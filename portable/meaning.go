package portable

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Answers are compared by meaning: 3.5 and 3.5000 are the same number,
// true and 1 the same truth, 0.30000000000000004 and 0.3 the same within
// floating point, and dates are the same however the engine prints the
// zone. Text, NULL and the empty string are compared exactly.

// cellsEqual reports whether two values mean the same.
func cellsEqual(a, b Cell) bool {
	if a.Null || b.Null {
		return a.Null && b.Null
	}
	if a.Kind == KindBytes || b.Kind == KindBytes {
		return a.Kind == b.Kind && strings.EqualFold(a.Text, b.Text)
	}
	if a.Kind == KindJSON || b.Kind == KindJSON {
		if ja, ok := parseJSON(a.Text); ok {
			if jb, ok := parseJSON(b.Text); ok {
				return jsonEqual(ja, jb)
			}
		}
		return a.Text == b.Text
	}
	if isDateKind(a.Kind) || isDateKind(b.Kind) {
		return normalizeDate(a.Text) == normalizeDate(b.Text)
	}
	if numericKind(a.Kind) != numericKind(b.Kind) {
		return false // text is never a number: '007' differs from 7
	}
	if numericKind(a.Kind) {
		x, okx := numberOf(a)
		y, oky := numberOf(b)
		if okx && oky {
			return numbersEqual(x, y, a.Kind == KindFloat || b.Kind == KindFloat)
		}
		return a.Text == b.Text
	}
	return a.Text == b.Text
}

func isDateKind(k Kind) bool { return k == KindDate || k == KindTime || k == KindTimestamp }

func numericKind(k Kind) bool {
	return k == KindInt || k == KindNumeric || k == KindFloat || k == KindBool
}

// numberOf reads a value as a number: t/f and true/false are 1 and 0.
func numberOf(c Cell) (*big.Float, bool) {
	t := strings.TrimSpace(c.Text)
	switch strings.ToLower(t) {
	case "t", "true":
		return big.NewFloat(1), true
	case "f", "false":
		return big.NewFloat(0), true
	case "inf", "infinity", "9.0e+999":
		return big.NewFloat(math.Inf(1)), true
	case "-inf", "-infinity", "-9.0e+999":
		return big.NewFloat(math.Inf(-1)), true
	}
	f, _, err := big.ParseFloat(t, 10, 200, big.ToNearestEven)
	if err != nil {
		return nil, false
	}
	return f, true
}

// numbersEqual compares exactly, or, when a float is involved, to about
// 12 significant digits (what a double keeps of a decimal literal).
func numbersEqual(x, y *big.Float, float bool) bool {
	if x.Cmp(y) == 0 {
		return true
	}
	if !float || x.IsInf() || y.IsInf() {
		return false
	}
	diff := new(big.Float).Sub(x, y)
	diff.Abs(diff)
	scale := new(big.Float).Abs(x)
	if ay := new(big.Float).Abs(y); ay.Cmp(scale) > 0 {
		scale = ay
	}
	if scale.Sign() == 0 {
		return diff.Sign() == 0
	}
	limit := new(big.Float).Mul(scale, big.NewFloat(1e-12))
	return diff.Cmp(limit) <= 0
}

// normalizeDate makes "2024-01-02T03:04:05.500000+00:00" and
// "2024-01-02 03:04:05.5" the same.
func normalizeDate(s string) string {
	s = strings.TrimSpace(strings.Replace(s, "T", " ", 1))
	for _, z := range []string{"+00:00", "+0000", "+00", "Z", " UTC"} {
		s = strings.TrimSuffix(s, z)
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && strings.Count(s, ":") >= 2 {
		frac := strings.TrimRight(s[i+1:], "0")
		if frac == "" {
			s = s[:i]
		} else {
			s = s[:i+1] + frac
		}
	}
	return s
}

func parseJSON(s string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if w, ok := y[k]; !ok || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		fx, _, e1 := big.ParseFloat(string(x), 10, 200, big.ToNearestEven)
		fy, _, e2 := big.ParseFloat(string(y), 10, 200, big.ToNearestEven)
		return e1 == nil && e2 == nil && fx.Cmp(fy) == 0
	}
	return a == b
}

// cellKey is a canonical form for sorting rows when order doesn't count.
// Numbers sort by value whatever their kind or spelling (the text '10', the
// integer 10 and the decimal 10.0 sit together), with the spelling as a
// tie-breaker so that both sides of a comparison sort alike.
func cellKey(c Cell) string {
	if c.Null {
		return "\x00"
	}
	if numericKind(c.Kind) || c.Kind == KindText && numericText(c.Text) {
		if f, ok := numberOf(c); ok {
			// Round floats so values that compare equal sort together.
			if v, _ := f.Float64(); c.Kind == KindFloat || c.Kind == KindText {
				return "n" + strconv.FormatFloat(v, 'g', 12, 64) + "\x00" + c.Text
			}
			return "n" + f.Text('g', 30) + "\x00" + c.Text
		}
	}
	if isDateKind(c.Kind) {
		return "d" + normalizeDate(c.Text)
	}
	return "s" + c.Text
}

func rowKey(r []Cell) string {
	var b bytes.Buffer
	for _, c := range r {
		b.WriteString(cellKey(c))
		b.WriteByte(0x1f)
	}
	return b.String()
}

// sortedRows returns the rows in canonical order.
func sortedRows(rows [][]Cell) [][]Cell {
	out := append([][]Cell(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool { return rowKey(out[i]) < rowKey(out[j]) })
	return out
}

func rowsEqual(a, b [][]Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if !cellsEqual(a[i][j], b[i][j]) {
				return false
			}
		}
	}
	return true
}

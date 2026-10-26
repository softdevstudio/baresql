package audit

// Result is everything one audit found. It is written as audit.json; the
// field names are a public interface (diff and --baseline read old files).
type Result struct {
	Tool      string       `json:"tool"`
	Version   string       `json:"version"`
	Generated string       `json:"generated"`
	Server    ServerReport `json:"server"`
	Settings  Settings     `json:"settings"`

	Relations         []RelationReport `json:"relations"`
	Logins            []LoginReport    `json:"logins"`
	Public            LoginReport      `json:"public"`
	DefaultPrivileges []DefaultPriv    `json:"default_privileges"`
	Findings          []Finding        `json:"findings"`
	NotSeen           []NotSeen        `json:"not_seen"`
	// Mismatches are places where BareSQL's reading of the catalogs and
	// the server's own privilege checks disagree. There should be none;
	// each one is a bug in BareSQL worth reporting.
	Mismatches []string `json:"mismatches"`
}

type ServerReport struct {
	Version    string `json:"version"`
	VersionNum int    `json:"version_num"`
	Database   string `json:"database"`
	Target     string `json:"target"` // connection without the password
	TLS        bool   `json:"tls"`
	Auth       string `json:"auth"`
	Auditor    string `json:"auditor"`
	Superuser  bool   `json:"auditor_is_superuser"`
}

type Settings struct {
	Sampled    bool     `json:"sampled"`
	SampleRows int      `json:"sample_rows"`
	Schemas    []string `json:"schemas"`
	Logins     []string `json:"logins,omitempty"` // when limited with --login
}

type RelationReport struct {
	Name            string         `json:"name"` // schema.name
	Kind            string         `json:"kind"`
	Owner           string         `json:"owner"`
	RowSecurity     string         `json:"row_security,omitempty"` // "", on, forced
	Policies        []PolicyReport `json:"policies,omitempty"`
	SecurityInvoker bool           `json:"security_invoker,omitempty"`
	Sampled         bool           `json:"sampled"`
	PartitionOf     string         `json:"partition_of,omitempty"`
	Columns         []ColumnReport `json:"columns"`
}

type PolicyReport struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Permissive bool     `json:"permissive"`
	Roles      []string `json:"roles"`
	Using      string   `json:"using,omitempty"`
	Check      string   `json:"check,omitempty"`
}

type ColumnReport struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Personal *Personal `json:"personal,omitempty"`
	// Sources: for view columns, the base columns the data comes from.
	Sources []string `json:"sources,omitempty"`
}

type LoginReport struct {
	Name string `json:"name"`
	// CannotConnect: the login may not connect to the audited database (no
	// CONNECT privilege, or a connection limit of 0); its routes say so and
	// give it nothing here.
	CannotConnect bool     `json:"cannot_connect,omitempty"`
	Attributes    []string `json:"attributes"` // superuser, createrole, bypassrls ...
	MemberOf      []string `json:"member_of"`  // direct memberships, with their options
	CanSetRole    []string `json:"can_set_role,omitempty"`
	Password      string   `json:"password,omitempty"` // md5, scram-sha-256, none (superuser auditors only)

	Read      []ColumnReach    `json:"read"`
	Write     []TableWrite     `json:"write"`
	Functions []FunctionAccess `json:"functions"`
	Server    []ServerAccess   `json:"server"`
	Mappings  []MappingAccess  `json:"foreign_servers"`
	Summary   LoginSummary     `json:"summary"`

	oid uint32 // the role, for findings; not in audit.json
}

type LoginSummary struct {
	Relations        int      `json:"relations"`         // relations with at least one readable column
	Columns          int      `json:"columns"`           // readable columns
	PersonalColumns  int      `json:"personal_columns"`  // readable base columns holding personal data
	PersonalTables   int      `json:"personal_tables"`   // base relations with readable personal data
	Categories       []string `json:"categories"`        // kinds of personal data it can read
	WritableTables   int      `json:"writable_tables"`   // relations it can change
	PersonalWritable int      `json:"personal_writable"` // of those, holding personal data
}

type ColumnReach struct {
	Relation string    `json:"relation"`
	Kind     string    `json:"kind"`
	Column   string    `json:"column"`
	Personal *Personal `json:"personal,omitempty"`
	Routes   []Route   `json:"routes"`
}

type TableWrite struct {
	Relation  string   `json:"relation"`
	Kind      string   `json:"kind"`
	Privilege string   `json:"privilege"`         // INSERT, UPDATE, DELETE, TRUNCATE
	Columns   []string `json:"columns,omitempty"` // empty: every column
	Personal  bool     `json:"personal"`
	Routes    []Route  `json:"routes"`
}

type FunctionAccess struct {
	Function        string  `json:"function"`
	Owner           string  `json:"owner"`
	SecurityDefiner bool    `json:"security_definer"`
	SearchPathSet   bool    `json:"search_path_set"`
	ServerAccess    bool    `json:"server_access"`
	Routes          []Route `json:"routes"`
}

type ServerAccess struct {
	Capability string  `json:"capability"` // read-server-files, write-server-files, run-server-programs, see-all-queries
	Routes     []Route `json:"routes"`
}

type MappingAccess struct {
	Server string  `json:"server"`
	FDW    string  `json:"fdw"`
	Routes []Route `json:"routes"`
}

type DefaultPriv struct {
	Creator   string `json:"creator"`
	Schema    string `json:"schema"` // "" for every schema
	Objects   string `json:"objects"`
	Privilege string `json:"privilege"`
	Grantee   string `json:"grantee"`
}

type Finding struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"` // high, medium, low
	Subject  string   `json:"subject"`  // the login, role, relation or function
	Message  string   `json:"message"`
	Details  []string `json:"details,omitempty"`
}

"""Hand-written parts of the rule posts on baresql.com: title, topic tag and
the opening paragraph. Everything else in a rule post comes from
test/results/portable (rules.json, rules/<code>.txt, examples/<code>.txt)."""

RULES = {
"integer-division": ("Integer Division Drops the Fraction on SQLite and PostgreSQL and Keeps It on MySQL", "numbers",
"You write an average as `SUM(stock) / COUNT(*)`, try it on SQLite and get 3. MySQL gives 3.3333 for the same rows. Both are doing what they were built to do, and they disagree about what dividing two integers means."),

"decimal-division": ("Dividing Decimals Gives Six Decimal Places on MySQL and Sixteen on PostgreSQL", "numbers",
"Split an invoice of 80.50 three ways and MySQL and MariaDB hand back 26.833333, PostgreSQL 26.8333333333333333 and SQLite a floating point number. Most of the time nobody notices. Then a report that adds the shares back up gets a different total on MySQL than on PostgreSQL."),

"pipes-concat": ("The Double Pipe Operator Joins Text on PostgreSQL and SQLite and Means OR on MySQL", "text",
"Code that builds labels with `first_name || ' ' || last_name` works on PostgreSQL and SQLite. Move it to MySQL or MariaDB and every label comes back as 0, with no error to say why."),

"concat-null": ("CONCAT With a NULL Argument Returns NULL on MySQL and Skips It on PostgreSQL", "text",
"A customer list built with `CONCAT(first_name, ' ', last_name)` looks complete on PostgreSQL and SQLite. Moved to MySQL or MariaDB, the same query returns NULL for some customers."),

"text-compare-case": ("MySQL Compares Text Without Regard to Letter Case and PostgreSQL Compares It Exactly", "text",
"Sign-ups as `ann@example.com` and `Ann@example.com` are two users on PostgreSQL and SQLite. On MySQL and MariaDB a unique key on the email column refuses the second sign-up."),

"like-case": ("LIKE Ignores Letter Case on SQLite and MySQL and Respects It on PostgreSQL", "text",
"A search box that runs `WHERE name LIKE 'ann%'` finds Ann on SQLite, MySQL and MariaDB. On PostgreSQL it misses her, because LIKE there compares letters exactly."),

"text-number-compare": ("Comparing a Text Column With the Number 0 Matches Codes Like abc on MySQL and Fails on PostgreSQL", "text",
"Product codes are text, and a query that forgets the quotes, `WHERE code = 0`, still runs on MySQL and MariaDB. There it returns codes that have nothing to do with 0."),

"double-quotes": ("Double Quotes Name a Column on PostgreSQL and Make a String on MySQL", "syntax",
"A query copied from PostgreSQL that puts a column name in double quotes runs on MySQL without an error, and never reads the column."),

"null-order": ("NULL Sorts First on SQLite and MySQL and Last on PostgreSQL", "NULL and sorting",
"Sort customers by the date of their last order and the ones who never ordered come first on SQLite, MySQL and MariaDB and last on PostgreSQL. The first page of the same report shows different people."),

"ungrouped-column": ("Columns Missing From GROUP BY Work on SQLite and MariaDB and Fail on PostgreSQL and MySQL", "queries",
"`SELECT customer_id, name, SUM(total) FROM orders GROUP BY customer_id` is the kind of query that runs for years on SQLite or MariaDB. Point the same code at PostgreSQL or MySQL and it stops with an error, because name is neither grouped nor summed."),

"cast-to-text": ("CAST to CHAR Cuts a Value to One Character on PostgreSQL", "text",
"`CAST(order_id AS CHAR)` looks like a safe way to turn a number into text. On PostgreSQL CHAR without a length means one character, so order 123 comes back as `'1'`."),

"cast-to-number": ("CAST to INTEGER Fails on MySQL and Rounds Differently on SQLite and PostgreSQL", "numbers",
"Turning a price into a whole number with `CAST(5.7 AS INTEGER)` gives 5 on SQLite and 6 on PostgreSQL and MariaDB. MySQL doesn't run it at all."),

"cast-to-date": ("CAST to DATE Returns the Number 2024 on SQLite Because It Has No Date Type", "dates",
"Filtering orders with `CAST(created_at AS DATE) = '2024-05-06'` works on PostgreSQL, MySQL and MariaDB. SQLite has no date type, turns the CAST into a number and keeps only the year, so the filter never matches."),

"date-arithmetic": ("Adding 1 to a Date Gives the Next Day on PostgreSQL and a Number on MySQL and SQLite", "dates",
"`due_date + 1` reads like the day after, and on PostgreSQL that is what it gives. On MySQL, MariaDB and SQLite the answer is a plain number."),

"division-by-zero": ("Division by Zero Is NULL on SQLite and MySQL and an Error on PostgreSQL", "numbers",
"A report that divides revenue by visits fails on PostgreSQL the first time a page has no visits. On SQLite, MySQL and MariaDB the same row quietly shows NULL."),

"decimal-as-float": ("SQLite Keeps DECIMAL Values as Floats and Sums of Prices Pick Up Rounding Errors", "numbers",
"A shop keeps its prices in a `DECIMAL(10, 2)` column and checks that an order's lines add up to its total. On SQLite that check can fail for an order of 0.10 and 0.20."),

"float-precision": ("REAL on PostgreSQL and FLOAT on MySQL Are 4-Byte Floats That Drop Digits", "numbers",
"Measurements kept in a REAL column on PostgreSQL, or in a FLOAT column on MySQL or MariaDB, come back with fewer digits than went in. The same type names keep them all on the other engines."),

"char-padding": ("CHAR Columns Come Back Padded With Spaces on PostgreSQL", "text",
"An order status kept in a `CHAR(10)` column compares as expected in SQL on every engine. In the application code, though, a check for paid orders fails on PostgreSQL alone."),

"length-bytes": ("LENGTH Counts Bytes on MySQL and MariaDB and Characters on PostgreSQL and SQLite", "text",
"A check that a name is at most 20 characters long, written with `LENGTH(name)`, lets 20 letters through on SQLite and PostgreSQL. MySQL and MariaDB count bytes instead, so a name with accents reaches the limit sooner."),

"lower-non-ascii": ("SQLite LOWER and UPPER Change Only the Letters A to Z", "text",
"Searching names with `LOWER(name) = LOWER('Émile')` works on PostgreSQL, MySQL and MariaDB. On SQLite, LOWER changes only A to Z, so É stays É and a stored émile never matches."),

"substring-start": ("SUBSTR From Position 0 Returns Text on SQLite and PostgreSQL and Nothing on MySQL", "text",
"Code ported from a language that counts from 0 often ends up with `SUBSTR(code, 0, 3)`. SQLite and PostgreSQL return the first two characters. MySQL and MariaDB return an empty string."),

"backslash-escapes": ("A Backslash Is an Escape Character in MySQL Strings and a Plain Character in PostgreSQL", "text",
"A Windows path written as `'C:\\temp\\new'` reads back on MySQL and MariaDB with a tab and a line break in it. PostgreSQL and SQLite keep the backslashes as typed."),

"like-escape": ("A Backslash in LIKE Escapes Percent and Underscore Everywhere Except SQLite", "text",
"Searching for the text 50_off with `LIKE '50\\_off'` works on PostgreSQL, MySQL and MariaDB, where the backslash escapes the underscore. SQLite treats the backslash as an ordinary character, and the search finds nothing."),

"log-base": ("LOG With One Argument Is Base 10 on PostgreSQL and the Natural Logarithm on MySQL", "numbers",
"A score or a scale built on `LOG()` shifts when the code moves to another engine, because `LOG(100)` doesn't mean the same logarithm everywhere."),

"caret-operator": ("The Caret Operator Is a Power on PostgreSQL and a Bitwise XOR on MySQL", "numbers",
"A growth formula written as `rate ^ years` works on PostgreSQL. On MySQL and MariaDB it still runs and gives numbers that have nothing to do with growth: `2 ^ 3` is 1 there."),

"dash-comment": ("Double Dash Comments Need a Space After Them on MySQL and MariaDB", "syntax",
"In a line like `SELECT price --discount`, left over from debugging, SQLite and PostgreSQL read `--discount` as a comment. MySQL and MariaDB add the discount to the price instead."),

"column-name-case": ("PostgreSQL Returns Column Names in Lower Case Unless You Quote Them", "syntax",
"`SELECT SUM(amount) AS Total` gives a column named Total on SQLite, MySQL and MariaDB. PostgreSQL calls it total."),

"table-name-case": ("MySQL and MariaDB on Linux Treat Table Names as Case Sensitive", "schemas",
"Code that creates a table as Users and later reads users works on SQLite and PostgreSQL. On MySQL or MariaDB running on Linux it fails, since users is another table that doesn't exist, while the same code on a Mac works, which makes the bug easy to miss."),

"type-not-enforced": ("SQLite Stores Text in INTEGER Columns and Ignores VARCHAR Lengths", "schemas",
"A test suite on SQLite passes with a quantity of `'abc'`, or `'Germany'` in a `VARCHAR(2)` country column. PostgreSQL, MySQL and MariaDB refuse both values, so the bug shows up in production instead."),

"boolean-as-number": ("Comparing a Boolean With 1 Works on MySQL and SQLite and Fails on PostgreSQL", "queries",
"`WHERE active = 1` is the usual way to test a flag on MySQL, and SQLite and MariaDB read it the same way. Moved to PostgreSQL, the query stops with an error."),

"auto-increment": ("No Auto Increment Column Syntax Works on Every SQL Engine", "schemas",
"A schema meant for several engines needs ids that number themselves, and no single way of asking for that works on all four. SERIAL comes closest, and on SQLite it fails without a word."),

"backticks": ("Backticks Around Names Work on MySQL and SQLite and Fail on PostgreSQL", "syntax",
"MySQL examples often quote every name in backticks, as in `` SELECT `name` FROM `people` ``. Copied into a PostgreSQL project, the query fails."),

"hash-comment": ("Hash Comments Work on MySQL and MariaDB and Become an Operator on PostgreSQL", "syntax",
"MySQL and MariaDB skip everything from a `#` to the end of the line. On SQLite and PostgreSQL that `#` is still part of the statement."),

"nulls-first-last": ("NULLS FIRST and NULLS LAST Fail on MySQL and MariaDB", "NULL and sorting",
"`ORDER BY shipped_at NULLS LAST` is the tidy way to put unshipped orders at the end on PostgreSQL and SQLite. MySQL and MariaDB have no such syntax and refuse the query."),

"limit-comma": ("LIMIT With a Comma Works on MySQL and SQLite and Fails on PostgreSQL", "queries",
"Paging code written for MySQL often says `LIMIT 20, 10`: skip 20 rows, return 10. SQLite and MariaDB read it the same way. PostgreSQL refuses it."),

"fetch-first": ("FETCH FIRST ROWS ONLY Works on PostgreSQL and MariaDB and Fails on MySQL and SQLite", "queries",
"`FETCH FIRST 10 ROWS ONLY` is the standard way to ask for the top ten, and PostgreSQL and MariaDB accept it. SQLite and MySQL refuse the query."),

"alias-in-where": ("Only SQLite Lets WHERE Use a Name From the Select List", "queries",
"A query that names `price * quantity` as total in the select list and then filters on total in WHERE runs on SQLite. PostgreSQL, MySQL and MariaDB refuse it, since the table has no column named total."),

"alias-in-having": ("PostgreSQL Refuses Select List Names in HAVING", "queries",
"A report of repeat customers names its count of orders n in the select list and filters on that name in HAVING. SQLite, MySQL and MariaDB run it, and PostgreSQL refuses the query."),

"limit-in-subquery": ("MySQL and MariaDB Refuse LIMIT Inside an IN Subquery", "queries",
"Finding the customers behind the ten latest orders with `WHERE id IN (SELECT customer_id FROM orders ORDER BY created_at DESC LIMIT 10)` works on SQLite and PostgreSQL. MySQL and MariaDB refuse it."),

"update-same-table": ("MySQL Refuses an UPDATE Whose Subquery Reads the Same Table", "inserts and updates",
"Marking rows with `UPDATE t SET flag = 1 WHERE id IN (SELECT id FROM t WHERE ...)` works on SQLite, PostgreSQL and MariaDB. MySQL refuses an UPDATE or DELETE whose subquery reads the table being changed."),

"update-join": ("UPDATE With a Join Needs Different Syntax on MySQL and PostgreSQL", "inserts and updates",
"Copying prices from a staging table with a join takes `UPDATE ... FROM` on SQLite and PostgreSQL and `UPDATE ... JOIN` on MySQL and MariaDB, and neither runs on all four."),

"full-outer-join": ("FULL OUTER JOIN Works on PostgreSQL and SQLite and Fails on MySQL and MariaDB", "queries",
"Matching two lists, such as invoices and payments, to see what is missing on either side calls for a FULL OUTER JOIN. PostgreSQL and SQLite run it, and MySQL and MariaDB refuse it."),

"distinct-on": ("DISTINCT ON Works on PostgreSQL Only", "queries",
"`DISTINCT ON (customer_id)` is a neat way to keep each customer's latest order on PostgreSQL, and none of the other three engines has it."),

"count-distinct-columns": ("COUNT DISTINCT With Two Columns Works on MySQL and Fails on PostgreSQL and SQLite", "queries",
"`COUNT(DISTINCT customer_id, product_id)` counts the distinct pairs on MySQL and MariaDB. SQLite and PostgreSQL take a single expression in `COUNT(DISTINCT ...)` and refuse it."),

"greatest-least": ("GREATEST Skips NULL on PostgreSQL and Returns NULL on MySQL", "NULL and sorting",
"`GREATEST(sale_price, list_price)` picks the larger of two prices. When one of them is NULL, PostgreSQL still picks the other price, and no other engine does."),

"insert-ignore": ("INSERT IGNORE Becomes INSERT OR IGNORE on SQLite and ON CONFLICT DO NOTHING on PostgreSQL", "inserts and updates",
"Loading a file of events that may hold rows you already have calls for an insert that skips duplicates. MySQL and MariaDB spell it `INSERT IGNORE` and SQLite `INSERT OR IGNORE`, while PostgreSQL and SQLite take `ON CONFLICT DO NOTHING`."),

"upsert": ("Upsert Needs ON CONFLICT on PostgreSQL and ON DUPLICATE KEY UPDATE on MySQL", "inserts and updates",
"Saving a setting that may or may not exist yet takes one statement on every engine, though not the same one on all four. SQLite and PostgreSQL use `ON CONFLICT ... DO UPDATE`, MySQL and MariaDB `ON DUPLICATE KEY UPDATE`."),

"returning": ("MySQL Refuses INSERT RETURNING While PostgreSQL and SQLite Accept It", "inserts and updates",
"`INSERT ... RETURNING id` hands back the new row's id in the same round trip on SQLite, PostgreSQL and MariaDB. MySQL has no RETURNING and refuses the statement."),

"varchar-length": ("VARCHAR Without a Length Fails on MySQL and MariaDB", "schemas",
"Schemas written for PostgreSQL often declare names and notes as plain `VARCHAR`, with no length. MySQL and MariaDB refuse that CREATE TABLE, since they want a length on every VARCHAR column."),

"type-not-everywhere": ("Column Types Like DATETIME and BYTEA Exist on Some SQL Engines Only", "schemas",
"A schema with DATETIME and JSONB columns runs on SQLite without complaint. Both types fail on at least one of the other engines."),

"table-options": ("MySQL Table Options Like ENGINE and CHARSET Fail on PostgreSQL and SQLite", "schemas",
"A MySQL schema ends its CREATE TABLE statements with options such as `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`. SQLite and PostgreSQL refuse the statement at the first option."),

"text-default": ("MySQL Refuses a Plain DEFAULT Value on TEXT Columns", "schemas",
"A notes column declared as `TEXT DEFAULT ''` works on SQLite, PostgreSQL and MariaDB. MySQL refuses the CREATE TABLE."),

"index-on-text": ("MySQL Refuses a Key on a TEXT Column Without a Prefix Length", "schemas",
"A TEXT column as a primary key is ordinary on SQLite and PostgreSQL. MySQL and MariaDB refuse it, and MySQL refuses a plain index or unique key on TEXT as well."),

"index-if-not-exists": ("MySQL Refuses CREATE INDEX IF NOT EXISTS", "schemas",
"Migrations that may run twice often say `CREATE INDEX IF NOT EXISTS`, and SQLite, PostgreSQL and MariaDB accept it. MySQL stops the migration with a syntax error."),

"truncate": ("SQLite Has No TRUNCATE Statement", "inserts and updates",
"Test setups often empty their tables with `TRUNCATE TABLE` between runs. PostgreSQL, MySQL and MariaDB have it, and SQLite refuses it."),

"create-or-replace": ("SQLite Has No CREATE OR REPLACE VIEW", "schemas",
"`CREATE OR REPLACE VIEW` lets a migration change a view in one step on PostgreSQL, MySQL and MariaDB. On SQLite the statement is a syntax error."),

"function-not-everywhere": ("SQL Functions That Exist on Some Engines Only and What to Write Instead", "functions and operators",
"IFNULL, NOW, LEFT and STRING_AGG are everyday functions, and each one is missing on at least one of SQLite, PostgreSQL, MySQL and MariaDB. BareSQL Portable knows {forms} functions like these."),

"operator-not-everywhere": ("SQL Operators That Exist on Some Engines Only and What to Write Instead", "functions and operators",
"Operators such as `::`, `ILIKE`, `DIV` and `XOR` belong to one or two engines, and the others refuse them. BareSQL Portable knows {forms} operators like these."),
}

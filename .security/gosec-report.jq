# Raw scanner output stays private. Publish only location/rule metadata.
if (.Stats.files | type) != "number" or .Stats.files <= 0
   or (.Issues | type) != "array"
   or (.["Golang errors"] | type) != "object"
   or (.["Golang errors"] | length) != 0 then
  error("incomplete gosec analysis")
else . end
| {
    stats: .Stats,
    issues: [.Issues[] | {
      rule: .rule_id, file: (.file | ltrimstr($root + "/")),
      line, severity, confidence
    }]
  }
| if any(.issues[];
    (.rule | test("^G[0-9]+$")) != true
    or (.file | startswith("/") or contains(".."))
    or (.line | test("^[0-9]+(-[0-9]+)?$")) != true
    or (.severity as $severity | ["LOW", "MEDIUM", "HIGH"] | index($severity)) == null
    or (.confidence as $confidence | ["LOW", "MEDIUM", "HIGH"] | index($confidence)) == null
  ) then error("invalid finding metadata") else . end

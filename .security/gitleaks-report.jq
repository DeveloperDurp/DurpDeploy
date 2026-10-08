if length != 1 or (.[0] | type) != "array" then
  error("expected exactly one report")
else .[0] end
| map({rule: .RuleID, file: .File, line: .StartLine, commit: .Commit})
| if any(.[];
    (.rule | type) != "string" or (.rule | length) == 0
    or (.file | type) != "string" or (.file | length) == 0
    or (.line | type) != "number" or .line < 1
    or (.commit | type) != "string"
  ) then error("invalid finding metadata") else . end

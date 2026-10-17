# Publish identifiers and locations, never source, messages, environment, or matches.
def text: type == "string" and length > 0;
if length != 1 or (.[0] | type) != "object" then error("invalid report") else .[0] end
| if .SchemaVersion != 2 or (.Results | type) != "array" or (.Results | length) == 0
  then error("missing scan results") else . end
| [.Results[] |
    if (.Target | text | not) or (.Type | text | not)
      then error("invalid target") else . end |
    {target: .Target, type: .Type,
     vulnerabilities: [(.Vulnerabilities // [])[] |
       if (.VulnerabilityID | text | not) or (.PkgName | text | not)
         or (.InstalledVersion | text | not) or (.FixedVersion != null and (.FixedVersion | type) != "string")
       then error("invalid vulnerability") else . end |
       {id: .VulnerabilityID, package: .PkgName, installed: .InstalledVersion,
        fixed: (.FixedVersion // ""), severity: .Severity}],
     misconfigurations: [(.Misconfigurations // [])[] |
       if (.ID | text | not) or (.Status as $status | ["PASS","FAIL","WARN"] | index($status) == null)
       then error("invalid check") else . end |
       {id: .ID, severity: .Severity, status: .Status,
        resource: .CauseMetadata.Resource, line: .CauseMetadata.StartLine}
       | with_entries(select(.value != null))]}]
| if any(.[]; any((.vulnerabilities[], .misconfigurations[]);
    .severity as $severity | ["UNKNOWN","LOW","MEDIUM","HIGH","CRITICAL"] | index($severity) == null))
  then error("invalid severity") else . end
| {results: ., blocking: ([.[] |
    (.vulnerabilities[] | select((.severity == "HIGH" or .severity == "CRITICAL") and .fixed != "")),
    (.misconfigurations[] | select((.severity == "HIGH" or .severity == "CRITICAL") and .status == "FAIL"))] | length)}

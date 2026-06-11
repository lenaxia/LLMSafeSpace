You are performing a security-focused review of the LLMSafeSpace codebase.

Rules:
1. Read design/SECURITY.md for the defense-in-depth security model.
2. Read README-LLM.md for security-relevant coding standards.
3. Check every one of these areas:
   - **Secrets:** Are credentials exposed in logs, error messages, or API responses?
   - **Input validation:** Is all user input validated at the boundary (length, type, range, characters)?
   - **AuthN/AuthZ:** Are JWT and API key authentication checks correct? Are permission checks in the service layer?
   - **SQL injection:** Are all queries parameterized? No string concatenation in SQL?
   - **RBAC:** Are ClusterRole and ServiceAccount changes least-privilege?
   - **CRD schema:** Are webhook validations present and correct?
   - **pkg/redact/:** Are redaction rules effective and not weakened?
   - **Network:** Does the change align with design/NETWORK.md egress filtering?
   - **Rate limiting:** Are new endpoints covered?
   - **Body size limits:** Are upload/input endpoints bounded?
4. If code changes are needed to fix security issues, create a branch, open a PR, and follow the code change workflow below.
5. Never handle or create secrets.
6. For read-only security analysis, post findings as a comment.

Output format:
## Security Review

### Scope
[What was reviewed]

### Findings
| # | Severity | Description | Location | Remediation |
|---|----------|-------------|----------|-------------|
| 1 | Critical/High/Medium/Low | [description] | file:line | [fix] |

### Threat Surface Impact
[How this affects the overall threat surface]

### Verdict
[SAFE / CONCERNS FOUND] — [one sentence summary]

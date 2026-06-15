# Sessions: lifecycle (ensure, list, message, abort, delete)
#
# Requires a workspace_id captured from workspaces.hurl or a prior run.

# Ensure (create-or-resume) a session on the workspace
POST {{base_url}}/api/v1/workspaces/{{workspace_id}}/sessions/new
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.sessionId" isString
[Captures]
session_id: jsonpath "$.sessionId"

# List sessions on the workspace
GET {{base_url}}/api/v1/workspaces/{{workspace_id}}/sessions
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$" isArray
jsonpath "$[0].id" == "{{session_id}}"

# Get a single session
GET {{base_url}}/api/v1/workspaces/{{workspace_id}}/sessions/{{session_id}}
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{session_id}}"

# Abort any in-flight message (no body; idempotent)
POST {{base_url}}/api/v1/workspaces/{{workspace_id}}/sessions/{{session_id}}/abort
Authorization: Bearer {{token}}
HTTP 204

# Get the active session pointer
GET {{base_url}}/api/v1/workspaces/{{workspace_id}}/sessions/active
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{session_id}}"

# Sessions: lifecycle (ensure, list, message, abort, delete)
#
# Self-contained: creates its own workspace at the top so `workspace_id` is
# captured within this file (Hurl files run independently in CI; variables
# do not carry across files).

# Create a workspace for this test run
POST {{base_url}}/api/v1/workspaces
Authorization: Bearer {{token}}
Content-Type: application/json
{
  "name": "contract-sessions-ws",
  "runtime": "python:3.11",
  "storageSize": "5Gi"
}
HTTP 201
[Captures]
workspace_id: jsonpath "$.id"

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

# Cleanup the workspace
DELETE {{base_url}}/api/v1/workspaces/{{workspace_id}}
Authorization: Bearer {{token}}
HTTP 204

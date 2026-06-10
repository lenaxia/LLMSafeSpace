# Workspaces: CRUD + lifecycle operations

# Create workspace
POST {{base_url}}/api/v1/workspaces
Authorization: Bearer {{token}}
Content-Type: application/json
{
  "name": "contract-test-ws",
  "runtime": "python:3.11",
  "storageSize": "5Gi"
}
HTTP 201
[Asserts]
jsonpath "$.id" isString
jsonpath "$.name" == "contract-test-ws"
jsonpath "$.runtime" == "python:3.11"
[Captures]
workspace_id: jsonpath "$.id"

# List workspaces
GET {{base_url}}/api/v1/workspaces?limit=10&offset=0
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.items" count >= 1

# Get workspace
GET {{base_url}}/api/v1/workspaces/{{workspace_id}}
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.id" == "{{workspace_id}}"
jsonpath "$.name" == "contract-test-ws"

# Rename workspace
PUT {{base_url}}/api/v1/workspaces/{{workspace_id}}
Authorization: Bearer {{token}}
Content-Type: application/json
{
  "name": "renamed-ws"
}
HTTP 204

# Get workspace status
GET {{base_url}}/api/v1/workspaces/{{workspace_id}}/status
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.phase" isString

# Suspend then re-activate workspace
POST {{base_url}}/api/v1/workspaces/{{workspace_id}}/suspend
Authorization: Bearer {{token}}
HTTP 202

POST {{base_url}}/api/v1/workspaces/{{workspace_id}}/activate
Authorization: Bearer {{token}}
HTTP 200

# Delete workspace
DELETE {{base_url}}/api/v1/workspaces/{{workspace_id}}
Authorization: Bearer {{token}}
HTTP 204

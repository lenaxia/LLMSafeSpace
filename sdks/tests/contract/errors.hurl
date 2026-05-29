# Error responses: verify consistent error format

# 401 - No auth header
GET {{base_url}}/api/v1/auth/me
HTTP 401
[Asserts]
jsonpath "$.error" isString

# 404 - Workspace not found
GET {{base_url}}/api/v1/workspaces/nonexistent-id-12345
Authorization: Bearer {{token}}
HTTP 404
[Asserts]
jsonpath "$.error" isString

# 400 - Invalid request body (register with missing fields)
POST {{base_url}}/api/v1/auth/register
Content-Type: application/json
{
  "username": "x"
}
HTTP 400
[Asserts]
jsonpath "$.error" isString

# 400 - Invalid workspace rename (missing name)
PUT {{base_url}}/api/v1/workspaces/some-id
Authorization: Bearer {{token}}
Content-Type: application/json
{}
HTTP 400
[Asserts]
jsonpath "$.error" isString

# Auth: Register + Login + API Key CRUD
# Run against: http://localhost:8080 (or mock server)

# Register
POST {{base_url}}/api/v1/auth/register
Content-Type: application/json
{
  "username": "testuser",
  "email": "test@example.com",
  "password": "securepass123"
}
HTTP 201
[Asserts]
jsonpath "$.token" isString
jsonpath "$.user.id" isString
jsonpath "$.user.email" == "test@example.com"

# Login
POST {{base_url}}/api/v1/auth/login
Content-Type: application/json
{
  "email": "test@example.com",
  "password": "securepass123"
}
HTTP 200
[Asserts]
jsonpath "$.token" isString
[Captures]
token: jsonpath "$.token"

# Get current user
GET {{base_url}}/api/v1/auth/me
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.id" isString
jsonpath "$.username" == "testuser"

# Create API key
POST {{base_url}}/api/v1/auth/api-keys
Authorization: Bearer {{token}}
Content-Type: application/json
{
  "name": "test-key"
}
HTTP 201
[Asserts]
jsonpath "$.id" isString
jsonpath "$.key" startsWith "lsp_"
jsonpath "$.name" == "test-key"
[Captures]
api_key_id: jsonpath "$.id"

# List API keys
GET {{base_url}}/api/v1/auth/api-keys
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$" count >= 1

# Delete API key
DELETE {{base_url}}/api/v1/auth/api-keys/{{api_key_id}}
Authorization: Bearer {{token}}
HTTP 204

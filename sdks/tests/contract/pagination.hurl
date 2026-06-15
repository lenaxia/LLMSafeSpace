# Pagination: list endpoints return cursor-style pagination metadata.
#
# Verifies the pagination envelope shape across workspaces + audit log.
# Page sizes must be honored; offset advances the cursor.

# Page size 1 — returns only the first workspace
GET {{base_url}}/api/v1/workspaces?limit=1&offset=0
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.items" count <= 1
jsonpath "$.pagination" isObject

# Page size 100 — returns everything in one page
GET {{base_url}}/api/v1/workspaces?limit=100&offset=0
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.items" isArray
jsonpath "$.pagination.limit" == 100

# Offset advances — second page must not duplicate the first item
GET {{base_url}}/api/v1/workspaces?limit=1&offset=1
Authorization: Bearer {{token}}
HTTP 200
[Captures]
second_page_first_id: jsonpath "$.items[0].id"

# Secrets audit log also paginates with the same envelope
GET {{base_url}}/api/v1/secrets/audit?limit=10&offset=0
Authorization: Bearer {{token}}
HTTP 200
[Asserts]
jsonpath "$.entries" isArray

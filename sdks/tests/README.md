# SDK Contract Tests

Language-agnostic contract tests defined as [Hurl](https://hurl.dev/) files. These verify that all SDKs produce correct HTTP requests and handle responses identically.

## Running

```bash
# Against a running API server
hurl --variable base_url=http://localhost:8080 --variable token=<jwt> contract/*.hurl

# Against Prism mock server (validates requests match OpenAPI spec)
npx @stoplight/prism-cli mock ../openapi.yaml --port 4010 &
hurl --variable base_url=http://localhost:4010 contract/errors.hurl
```

## Test Files

| File | Coverage |
|------|----------|
| `auth.hurl` | Register, login, API key CRUD, auth header format |
| `workspaces.hurl` | Create, list, get, rename, suspend, resume, delete |
| `errors.hurl` | 400, 401, 404 error format consistency |

## Contract Guarantees

All SDKs must produce HTTP traffic equivalent to these Hurl files:
1. Correct URL paths and HTTP methods
2. Correct request body JSON structure
3. Correct Authorization header format (`Bearer <token>`)
4. Correct handling of 2xx, 4xx response codes
5. Correct parsing of response JSON into typed objects

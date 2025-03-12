package docs

import (
	"github.com/swaggo/swag"
)

// @title LLMSafeSpace API
// @version 1.0
// @description API for secure code execution in isolated environments.
// @termsOfService https://llmsafespace.dev/terms/

// @contact.name API Support
// @contact.url https://llmsafespace.dev/support
// @contact.email support@llmsafespace.dev

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host api.llmsafespace.dev
// @BasePath /api/v1
// @schemes https

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name Authorization
// @description API key authentication. Format: "Bearer {api_key}"

// @securityDefinitions.oauth2.implicit OAuth2
// @authorizationUrl https://llmsafespace.dev/oauth/authorize
// @scope.read Grants read access
// @scope.write Grants write access
// @scope.admin Grants admin access

// @tag.name Sandboxes
// @tag.description Sandbox management endpoints

// @tag.name WarmPools
// @tag.description Warm pool management endpoints

// @tag.name Runtimes
// @tag.description Runtime environment endpoints

// @tag.name Profiles
// @tag.description Security profile endpoints

// @tag.name Users
// @tag.description User management endpoints

// @tag.name Auth
// @tag.description Authentication endpoints

// SwaggerInfo holds the API information used by the swagger specification
var SwaggerInfo = &swag.Spec{
	Version:          "1.0",
	Host:             "api.llmsafespace.dev",
	BasePath:         "/api/v1",
	Schemes:          []string{"https"},
	Title:            "LLMSafeSpace API",
	Description:      "API for secure code execution in isolated environments.",
	InfoInstanceName: "swagger",
	SwaggerTemplate:  docTemplate,
}

const docTemplate = `{
    "swagger": "2.0",
    "info": {
        "description": "{{.Description}}",
        "title": "{{.Title}}",
        "termsOfService": "https://llmsafespace.dev/terms/",
        "contact": {
            "name": "API Support",
            "url": "https://llmsafespace.dev/support",
            "email": "support@llmsafespace.dev"
        },
        "license": {
            "name": "Apache 2.0",
            "url": "http://www.apache.org/licenses/LICENSE-2.0.html"
        },
        "version": "{{.Version}}"
    },
    "host": "{{.Host}}",
    "basePath": "{{.BasePath}}",
    "paths": {}
}`

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}

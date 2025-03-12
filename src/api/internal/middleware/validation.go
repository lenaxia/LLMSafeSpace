package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// Use a single instance of Validate, it caches struct info
var validate = validator.New()

// ValidationMiddleware returns a middleware that validates request bodies
func ValidationMiddleware(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip validation for GET, DELETE, and OPTIONS requests
		if c.Request.Method == http.MethodGet || 
		   c.Request.Method == http.MethodDelete || 
		   c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		
		// Get the model to validate from the context
		model, exists := c.Get("validationModel")
		if !exists {
			c.Next()
			return
		}
		
		// Read request body
		var bodyBytes []byte
		if c.Request.Body != nil {
			bodyBytes, _ = io.ReadAll(c.Request.Body)
			// Restore the body
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
		
		// Skip if no body
		if len(bodyBytes) == 0 {
			c.Next()
			return
		}
		
		// Create a new instance of the model
		modelType := reflect.TypeOf(model)
		if modelType.Kind() == reflect.Ptr {
			modelType = modelType.Elem()
		}
		modelValue := reflect.New(modelType).Interface()
		
		// Unmarshal the request body into the model
		if err := json.Unmarshal(bodyBytes, modelValue); err != nil {
			apiErr := errors.NewValidationError("Invalid request body", map[string]interface{}{
				"error": err.Error(),
			}, err)
			HandleAPIError(c, apiErr)
			return
		}
		
		// Validate the model
		if err := validate.Struct(modelValue); err != nil {
			// Convert validation errors to a map
			validationErrors := make(map[string]string)
			for _, err := range err.(validator.ValidationErrors) {
				validationErrors[err.Field()] = getValidationErrorMessage(err)
			}
			
			apiErr := errors.NewValidationError("Validation failed", map[string]interface{}{
				"errors": validationErrors,
			}, err)
			HandleAPIError(c, apiErr)
			return
		}
		
		// Store the validated model in the context
		c.Set("validatedModel", modelValue)
		
		c.Next()
	}
}

// ValidateRequest validates a request body against a model
func ValidateRequest(c *gin.Context, model interface{}) error {
	// Set the validation model in the context
	c.Set("validationModel", model)
	
	// Read request body
	var bodyBytes []byte
	if c.Request.Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request.Body)
		// Restore the body
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}
	
	// Skip if no body
	if len(bodyBytes) == 0 {
		return errors.NewValidationError("Request body is required", nil, nil)
	}
	
	// Unmarshal the request body into the model
	if err := json.Unmarshal(bodyBytes, model); err != nil {
		return errors.NewValidationError("Invalid request body", map[string]interface{}{
			"error": err.Error(),
		}, err)
	}
	
	// Validate the model
	if err := validate.Struct(model); err != nil {
		// Convert validation errors to a map
		validationErrors := make(map[string]string)
		for _, err := range err.(validator.ValidationErrors) {
			validationErrors[err.Field()] = getValidationErrorMessage(err)
		}
		
		return errors.NewValidationError("Validation failed", map[string]interface{}{
			"errors": validationErrors,
		}, err)
	}
	
	return nil
}

// getValidationErrorMessage returns a human-readable error message for a validation error
func getValidationErrorMessage(err validator.FieldError) string {
	switch err.Tag() {
	case "required":
		return "This field is required"
	case "email":
		return "Invalid email address"
	case "min":
		return "Value must be greater than or equal to " + err.Param()
	case "max":
		return "Value must be less than or equal to " + err.Param()
	case "len":
		return "Length must be " + err.Param()
	case "oneof":
		return "Value must be one of " + err.Param()
	default:
		return "Invalid value"
	}
}

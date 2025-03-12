package session

import (
	"encoding/json"
)

// Message represents a WebSocket message
type Message struct {
	Type        string      `json:"type"`
	ID          string      `json:"id,omitempty"`
	Stream      string      `json:"stream,omitempty"`
	Content     string      `json:"content,omitempty"`
	Code        string      `json:"code,omitempty"`
	Message     string      `json:"message,omitempty"`
	ExitCode    int         `json:"exitCode,omitempty"`
	Timestamp   int64       `json:"timestamp,omitempty"`
	Data        interface{} `json:"data,omitempty"`
}

// ParseMessage parses a JSON message
func ParseMessage(data []byte) (Message, error) {
	var msg Message
	err := json.Unmarshal(data, &msg)
	return msg, err
}

// GetString gets a string value from the Data field
func (m *Message) GetString(key string) (string, bool) {
	if m.Data == nil {
		return "", false
	}
	
	data, ok := m.Data.(map[string]interface{})
	if !ok {
		return "", false
	}
	
	value, ok := data[key].(string)
	return value, ok
}

// GetInt gets an int value from the Data field
func (m *Message) GetInt(key string) (int, bool) {
	if m.Data == nil {
		return 0, false
	}
	
	data, ok := m.Data.(map[string]interface{})
	if !ok {
		return 0, false
	}
	
	valueFloat, ok := data[key].(float64)
	if ok {
		return int(valueFloat), true
	}
	
	valueInt, ok := data[key].(int)
	return valueInt, ok
}

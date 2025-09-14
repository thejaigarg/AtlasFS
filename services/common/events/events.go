// services/common/events/events.go
package events

import (
	"encoding/json"
	"fmt"
	"time"
)

type EventType string

const (
	FileUploadStarted   EventType = "file.upload.started"
	FileUploadCompleted EventType = "file.upload.completed"
	FileChunkCreated    EventType = "file.chunk.created"
	FileChunkStored     EventType = "file.chunk.stored"
	FileDeleted         EventType = "file.deleted"
)

type Event struct {
	ID        string                 `json:"id"`
	Type      EventType              `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Source    string                 `json:"source"`
	Data      map[string]interface{} `json:"data"`
}

// This signature matches what your gateway expects: (eventType, source, data)
func NewEvent(eventType EventType, source string, data map[string]interface{}) *Event {
	return &Event{
		ID:        fmt.Sprintf("evt_%d", time.Now().UnixNano()),
		Type:      eventType,
		Timestamp: time.Now(),
		Source:    source,
		Data:      data,
	}
}

// Add the ToJSON method that gateway uses
func (e *Event) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

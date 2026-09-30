package mcpserver

import (
	"fmt"
	"net/url"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// inputSchema is the schema the SDK would infer for In, narrowed where the API
// is narrower than a Go type can say: an engine is one of nine words, a port
// is between 1 and 65535.
//
// Written into the schema, the choices are in front of the assistant before it
// calls, rather than in an error after it guessed "postgresql"; and a wrong
// one is refused by the SDK, naming the choices, before the panel is asked.
func inputSchema[In any](narrow func(properties map[string]*jsonschema.Schema)) *jsonschema.Schema {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		// A type that cannot be described is a mistake in this package, and
		// every test that builds a server finds it.
		panic(fmt.Sprintf("describe the input of %s: %v", reflect.TypeFor[In](), err))
	}
	narrow(schema.Properties)
	return schema
}

// oneOf limits a property to a fixed set of words.
func oneOf(property *jsonschema.Schema, values ...string) {
	property.Enum = make([]any, 0, len(values))
	for _, value := range values {
		property.Enum = append(property.Enum, value)
	}
}

// portRange limits a property to a port number.
func portRange(property *jsonschema.Schema) {
	property.Minimum, property.Maximum = new(1.0), new(65535.0)
}

// appPath is a route under one app.
//
// The id is escaped because a model wrote it. An id with a slash in it would
// otherwise be a different route — "app_1/domains" and a DELETE is not the
// request anybody asked for — where escaped it is one segment the panel does
// not know, and answers as such.
func appPath(appID, rest string) string {
	return "/api/apps/" + url.PathEscape(appID) + rest
}

// databasePath is a route under one database, escaped for the same reason.
func databasePath(databaseID, rest string) string {
	return "/api/databases/" + url.PathEscape(databaseID) + rest
}

// doneOutput is the answer of a tool whose only news is that it worked.
type doneOutput struct {
	Note string `json:"note"`
}

func done(note string) (*mcp.CallToolResult, doneOutput, error) {
	return textResult(note), doneOutput{Note: note}, nil
}

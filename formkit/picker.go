package formkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

// hostKeys are the keys the host and the button add to a meta call on top of
// the form's own fields. They are not form data and must not be echoed back
// into it — `settings` above all, which carries the credentials of the target
// system and would be persisted onto the node.
var hostKeys = map[string]bool{"settings": true, "value": true, "targetField": true, "form": true}

// FormData echoes a meta call's form back unchanged, minus what the host and
// the button added.
//
// A form envelope replaces the form's data wholesale, so the echo has to be
// exact. Read the call as a map rather than a typed struct for the same reason:
// a picker cannot echo fields it never decoded.
func FormData(call map[string]any) map[string]any {
	data := make(map[string]any, len(call))
	for key, value := range call {
		if !hostKeys[key] {
			data[key] = value
		}
	}
	return data
}

// Choices rewrites one property of a JSON Schema into a drop-down of the given
// candidates, returning the whole schema with that one change.
//
// It takes the schema as text, so it works on any form — one built by this
// package, or one a plugin hand-wrote years ago.
//
// `oneOf` rather than `enum` because each candidate needs two halves: the value
// the API wants, and the label a human recognises. Any `enum` already on the
// property is dropped, since the two would otherwise be intersected and the
// result would be empty.
func Choices(schemaJSON, target string, options []Option) (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		return nil, fmt.Errorf("formkit: form schema does not parse: %w", err)
	}

	properties, _ := schema["properties"].(map[string]any)
	property, _ := properties[target].(map[string]any)
	if property == nil {
		return nil, fmt.Errorf("formkit: the form has no property %q to turn into a drop-down", target)
	}

	property["oneOf"] = oneOf(options)
	delete(property, "enum")
	return schema, nil
}

// Picker answers an ambiguous lookup with a form the user can choose from.
//
// A field's options live in its JSON Schema, not in the form's data, so the
// only way to offer a list that did not exist when the plugin was compiled is
// to answer with a whole new schema. That is what a *form envelope* is: the
// host re-renders the open dialog as the documents returned here, and the field
// the button drives becomes a drop-down of exactly what the lookup found.
//
// The rebuilt schema is the action's own with one property changed, so
// everything else — the other fields, their titles, what is required — survives
// untouched. `data` is the form as it currently stands ([FormData]), because
// the envelope replaces the data as well as the documents.
//
// The returned map carries envelope keys only. The host tells a re-render from
// a field patch by requiring every key to be one of them, so a stray field name
// here demotes the whole answer back to a patch and nothing re-renders. The
// heading rides along under [NotifKey], which is one of them.
func Picker(form sdkv1.FormBuilder, target string, options []Option, data map[string]any, heading Notification) (map[string]any, error) {
	if form.Jsonschema == "" {
		return nil, errors.New("formkit: cannot rebuild a form that has no schema")
	}

	schema, err := Choices(form.Jsonschema, target, options)
	if err != nil {
		return nil, err
	}

	envelope := map[string]any{
		"schema":   schema,
		"uischema": form.Jsonui,
		"data":     data,
	}
	if heading.Message != "" {
		envelope[NotifKey] = heading
	}
	return envelope, nil
}

// Choose is [Picker] with the fallback every caller wants: when the form cannot
// be rebuilt — the action was named wrong, the target is not one of its
// properties — the candidates are reported as text instead.
//
// That fallback matters because the alternative is a button that appears to do
// nothing. A list the user has to read and type from is a worse answer than a
// drop-down, but it is still an answer.
//
// The return type is `any` because the two outcomes are different shapes on the
// wire, which is also why a meta handler returns `any`.
func Choose(form sdkv1.FormBuilder, target string, options []Option, data map[string]any, heading Notification) any {
	envelope, err := Picker(form, target, options, data, heading)
	if err != nil {
		message := strings.TrimRight(heading.Message, "\n")
		return Notification{
			Severity: orDefault(heading.Severity, "info"),
			Field:    heading.Field,
			Message:  strings.TrimSpace(message + "\n" + Lines(options)),
		}.Patch(nil)
	}
	return envelope
}

// listed caps how many candidates a text fallback prints, so a broad search
// does not paste a hundred rows into a form.
const listed = 15

// Lines renders candidates one per line — the text form of a picker, for the
// fallback above and for any handler that would rather say what it found than
// rebuild the dialog.
func Lines(options []Option) string {
	lines := make([]string, 0, listed+1)
	for _, option := range options[:min(len(options), listed)] {
		label := option.Label
		if label == "" {
			label = option.Value
		}
		lines = append(lines, "  "+label)
	}
	if len(options) > listed {
		lines = append(lines, fmt.Sprintf("  … and %d more — narrow the search", len(options)-listed))
	}
	return strings.Join(lines, "\n")
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

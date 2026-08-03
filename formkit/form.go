// Package formkit builds the two documents an Inflowenger form is made of — a
// JSON Schema for the data and a JSON Forms UI Schema for the layout — from a
// single declaration of each field.
//
// Hand-written, those two documents drift: a property gets renamed in one and
// not the other, a control points at a scope that no longer exists, and the
// field silently stops rendering. Declaring a field once and generating both
// removes that class of bug, and keeps the order of the form in the order of
// the code.
//
// The package is additive and optional. Nothing in sdkv1 imports it, and what
// it produces is ordinary JSON Schema + UI Schema text, so a plugin may:
//
//   - build every form with it,
//   - build one form with it and hand-write the next,
//   - or ignore the builder entirely and use only [Picker] / [FormData] /
//     [Notification], which work against raw schema strings a plugin wrote by
//     hand — including forms written before this package existed.
//
// A form is fields in declaration order, optionally grouped:
//
//	form := formkit.New("Create issue").Add(
//		formkit.Text("projectKey", "Project key").
//			Describe("e.g. OPS — or the numeric project id").
//			Required().
//			Lookup("jira.meta.project.resolve", "Find").Picks("jira.issue.create"),
//		formkit.Text("summary", "Summary").Required(),
//		formkit.TextArea("description", "Description"),
//	).Build()
//
//	p.AddAction(sdkv1.Action{Method: "jira.issue.create", Form: form, ...})
//
// Property order lives in the UI Schema, which is where a renderer reads it;
// the `properties` object itself is a JSON object and its key order is not
// meaningful.
package formkit

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

// Form is a form under construction: the fields it holds, in the order they
// were added, and the sections they are laid out in.
//
// The zero value is not useful — start from [New].
type Form struct {
	title       string
	description string
	submitTo    string
	sections    []section
}

// section is one run of fields. An untitled section renders its controls
// straight into the top-level layout; a titled one renders them inside a Group,
// which is how JSON Forms draws a labelled box around part of a form.
type section struct {
	title  string
	fields []*Field
}

// New starts a form. The title is the JSON Schema `title`, which renderers show
// as the heading of the dialog.
func New(title string) *Form { return &Form{title: title} }

// Describe sets the schema's `description` — a line under the heading saying
// what the whole form is for.
func (f *Form) Describe(text string) *Form {
	f.description = text
	return f
}

// SubmitTo names the meta function the host calls to validate the form when it
// is submitted (sdkv1.FormBuilder.SubmitTo). That handler answers with an
// sdkv1.Response, unlike the button handlers described on [Field.Lookup].
func (f *Form) SubmitTo(method string) *Form {
	f.submitTo = method
	return f
}

// Add appends fields to the form, in the order they will be rendered.
func (f *Form) Add(fields ...*Field) *Form {
	if n := len(f.sections); n > 0 && f.sections[n-1].title == "" {
		f.sections[n-1].fields = append(f.sections[n-1].fields, fields...)
		return f
	}
	f.sections = append(f.sections, section{fields: fields})
	return f
}

// Group appends a labelled section. Its fields are ordinary properties of the
// same flat schema — the grouping is layout only, so the data the action
// receives has no extra nesting.
func (f *Form) Group(title string, fields ...*Field) *Form {
	f.sections = append(f.sections, section{title: title, fields: fields})
	return f
}

// Fields returns every field in declaration order, groups flattened.
func (f *Form) Fields() []*Field {
	out := make([]*Field, 0, 8)
	for _, s := range f.sections {
		out = append(out, s.fields...)
	}
	return out
}

// Validate reports what would make the generated documents wrong: a field with
// no name, a name used twice, or a custom schema fragment that cannot be
// marshalled. [Form.Build] runs it and panics on failure, so calling this
// directly is only needed when a form is assembled from data rather than
// written out in code.
func (f *Form) Validate() error {
	seen := make(map[string]bool)
	for _, field := range f.Fields() {
		switch {
		case field == nil:
			return fmt.Errorf("formkit: form %q has a nil field", f.title)
		case strings.TrimSpace(field.name) == "":
			return fmt.Errorf("formkit: form %q has a field with no name", f.title)
		case seen[field.name]:
			return fmt.Errorf("formkit: form %q declares %q twice", f.title, field.name)
		}
		seen[field.name] = true

		if _, err := json.Marshal(field.schema); err != nil {
			return fmt.Errorf("formkit: field %q has a schema that will not marshal: %w", field.name, err)
		}
	}
	return nil
}

// Schema returns the JSON Schema document as text.
func (f *Form) Schema() string { return mustEncode(f.SchemaMap()) }

// UI returns the JSON Forms UI Schema document as text.
func (f *Form) UI() string { return mustEncode(f.UIMap()) }

// SchemaMap returns the JSON Schema as a map, for the callers that go on to
// edit it — [Picker] rebuilding one property as a drop-down, or a plugin
// splicing in a fragment this package has no vocabulary for.
func (f *Form) SchemaMap() map[string]any {
	properties := make(map[string]any)
	required := make([]any, 0, 4)

	for _, field := range f.Fields() {
		properties[field.name] = field.schema
		if field.required {
			required = append(required, field.name)
		}
	}

	schema := map[string]any{"type": "object", "properties": properties}
	if f.title != "" {
		schema["title"] = f.title
	}
	if f.description != "" {
		schema["description"] = f.description
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// UIMap returns the UI Schema as a map.
func (f *Form) UIMap() map[string]any {
	elements := make([]any, 0, len(f.sections))
	for _, s := range f.sections {
		controls := make([]any, 0, len(s.fields))
		for _, field := range s.fields {
			controls = append(controls, field.control())
		}
		if s.title == "" {
			elements = append(elements, controls...)
			continue
		}
		elements = append(elements, map[string]any{
			"type":     "Group",
			"label":    s.title,
			"elements": controls,
		})
	}
	return map[string]any{"type": "VerticalLayout", "elements": elements}
}

// Build renders the form into the sdkv1.FormBuilder an action or a settings
// profile carries.
//
// It panics if [Form.Validate] fails. Forms are declared at start-up from
// literals, so a failure here is a programming error that would otherwise reach
// the user as a dialog that will not render.
func (f *Form) Build() sdkv1.FormBuilder {
	if err := f.Validate(); err != nil {
		panic(err.Error())
	}
	return sdkv1.FormBuilder{
		SubmitTo:   f.submitTo,
		Jsonschema: f.Schema(),
		Jsonui:     f.UI(),
	}
}

// Settings renders the form as a plugin settings profile: the same two
// documents, plus the handler the host calls when the profile is submitted.
//
// The handler is a validator, not a store. The platform keeps the profile and
// ships it back with every call as body.settings, so a plugin that saves it
// anywhere is keeping a second, staler copy of someone's credentials.
func (f *Form) Settings(submit func(sdkv1.Request) sdkv1.Response) *sdkv1.Settings {
	return &sdkv1.Settings{FormBuilder: f.Build(), SubmitHandler: submit}
}

// mustEncode marshals a document built entirely out of maps, slices and
// scalars. Anything that can fail here has already been caught by Validate.
func mustEncode(document map[string]any) string {
	encoded, err := json.Marshal(document)
	if err != nil {
		panic("formkit: encode: " + err.Error())
	}
	return string(encoded)
}

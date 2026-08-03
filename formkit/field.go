package formkit

// Field is one property of a form: its JSON Schema entry, and the UI Schema
// control that renders it. Both are generated from this one declaration, so a
// control can never point at a property that is not there.
//
// Fields are built by the constructors below and configured by chaining, each
// method returning the same field:
//
//	formkit.Integer("maxResults", "Max results").Default(50).Between(1, 100)
//
// Anything this package has no word for goes in verbatim with [Field.Set] (JSON
// Schema) or [Field.Option] (renderer hints), so an unusual field never forces
// a plugin back to hand-written JSON for the whole form.
type Field struct {
	name     string
	schema   map[string]any
	options  map[string]any
	inflowUI map[string]any
	notifs   []Notification
	rule     map[string]any
	required bool
}

// ------------------------------------------------------------ constructors --

func field(name, title, jsonType string) *Field {
	f := &Field{name: name, schema: map[string]any{"type": jsonType}}
	if title != "" {
		f.schema["title"] = title
	}
	return f
}

// Text is a single-line string.
func Text(name, title string) *Field { return field(name, title, "string") }

// TextArea is a string rendered as a multi-line box. Use it wherever a value
// can reasonably contain a newline — a comment body, a JSON fragment, a list of
// matches a lookup reported back.
func TextArea(name, title string) *Field {
	return Text(name, title).Option("multi", true)
}

// Secret is a string rendered with its characters masked.
//
// Masking is presentation only: the value travels and is stored like any other
// field. It belongs on a settings profile, which the platform holds, and not on
// an action form, whose data is saved onto the node in the flow.
func Secret(name, title string) *Field {
	return Text(name, title).Option("format", "password")
}

// Integer is a whole number. JSON Schema distinguishes it from Number, and a
// renderer that knows the difference will refuse a decimal point in the box
// rather than at submit.
func Integer(name, title string) *Field { return field(name, title, "integer") }

// Number is a decimal number.
func Number(name, title string) *Field { return field(name, title, "number") }

// Bool is a checkbox.
func Bool(name, title string) *Field { return field(name, title, "boolean") }

// Date is a string holding a calendar date, YYYY-MM-DD.
func Date(name, title string) *Field { return Text(name, title).Format("date") }

// DateTime is a string holding an RFC 3339 instant.
func DateTime(name, title string) *Field { return Text(name, title).Format("date-time") }

// Enum is a fixed set of values, rendered as a drop-down. Use it when the value
// the API wants is the one a human should read; when they differ, use [Choice].
func Enum(name, title string, values ...string) *Field {
	f := Text(name, title)
	choices := make([]any, 0, len(values))
	for _, value := range values {
		choices = append(choices, value)
	}
	return f.Set("enum", choices)
}

// Choice is a drop-down whose entries have two halves: the value the API needs,
// and the label a human recognises. It is `oneOf` rather than `enum` because an
// enum can only carry one of the two.
func Choice(name, title string, options ...Option) *Field {
	return Text(name, title).Set("oneOf", oneOf(options))
}

// List is an array of strings — the renderer draws add/remove rows.
func List(name, title string) *Field { return ListOf(name, title, "string") }

// ListOf is an array whose items are of the given JSON type.
func ListOf(name, title, itemType string) *Field {
	return field(name, title, "array").Set("items", map[string]any{"type": itemType})
}

// Custom is a field whose schema this package does not model: pass the JSON
// Schema fragment for the property and it is used as-is, while the control, the
// layout position, the lookup button and the messages are still generated.
//
// The fragment is taken over, not copied — do not keep editing the map after
// handing it in.
func Custom(name, title string, schema map[string]any) *Field {
	if schema == nil {
		schema = map[string]any{}
	}
	f := &Field{name: name, schema: schema}
	if title != "" {
		f.schema["title"] = title
	}
	return f
}

// Name is the property name this field writes into.
func (f *Field) Name() string { return f.name }

// -------------------------------------------------------------- the schema --

// Describe sets the property's `description`: a statement of what the field is.
// Advice about *using* it — press this, fill that in first — reads better as
// [Field.Help], which renderers show as a message rather than as label text.
func (f *Field) Describe(text string) *Field { return f.Set("description", text) }

// Required adds the field to the schema's `required` list.
//
// Required means the form cannot be submitted without it, so it is for what the
// plugin genuinely cannot run without. A value that is merely usually needed is
// better left optional with a description saying when it matters.
func (f *Field) Required() *Field {
	f.required = true
	return f
}

// Default is the value the form starts with. It is also what the action
// receives when the user never touches the field, so it should be the choice
// that is right most of the time rather than a placeholder.
func (f *Field) Default(value any) *Field { return f.Set("default", value) }

// Format sets the JSON Schema `format` — date, date-time, uri, email …
func (f *Field) Format(format string) *Field { return f.Set("format", format) }

// Min sets the smallest accepted number.
func (f *Field) Min(value any) *Field { return f.Set("minimum", value) }

// Max sets the largest accepted number.
func (f *Field) Max(value any) *Field { return f.Set("maximum", value) }

// Between bounds a number on both sides.
func (f *Field) Between(min, max any) *Field { return f.Min(min).Max(max) }

// Set writes a JSON Schema keyword verbatim — pattern, minLength, items, and
// anything else this package has no method for.
func (f *Field) Set(key string, value any) *Field {
	f.schema[key] = value
	return f
}

// ------------------------------------------------------------------ the UI --

// Option sets a JSON Forms renderer hint under the control's `options`, e.g.
// "multi" for a text area or "slider" for a bounded number.
func (f *Field) Option(key string, value any) *Field {
	if f.options == nil {
		f.options = map[string]any{}
	}
	f.options[key] = value
	return f
}

// ShowWhen renders this field only while another field holds the given value —
// the visibility rule JSON Forms evaluates in the browser, with no round trip.
//
// It hides; it does not exclude. A hidden field keeps whatever value it already
// had, and that value is still submitted, so a rule is a way to keep a form
// short and not a way to enforce which fields may be set together.
func (f *Field) ShowWhen(other string, is any) *Field { return f.when("SHOW", other, is) }

// HideWhen is [Field.ShowWhen] inverted: the field disappears while the other
// field holds that value.
func (f *Field) HideWhen(other string, is any) *Field { return f.when("HIDE", other, is) }

// EnableWhen leaves the field on screen but greys it out until the other field
// holds the given value. Prefer it to ShowWhen when the field's absence would
// be confusing — a user looking for it can see that it exists and what unlocks it.
func (f *Field) EnableWhen(other string, is any) *Field { return f.when("ENABLE", other, is) }

func (f *Field) when(effect, other string, is any) *Field {
	f.rule = map[string]any{
		"effect": effect,
		"condition": map[string]any{
			"scope":  scopeOf(other),
			"schema": map[string]any{"const": is},
		},
	}
	return f
}

// scopeOf is the JSON-pointer-ish reference a UI Schema uses to name a
// property. A caller that already wrote one out in full keeps it.
func scopeOf(name string) string {
	if len(name) > 0 && name[0] == '#' {
		return name
	}
	return "#/properties/" + name
}

// control renders the field's UI Schema element.
func (f *Field) control() map[string]any {
	element := map[string]any{"type": "Control", "scope": scopeOf(f.name)}
	if len(f.options) > 0 {
		element["options"] = f.options
	}
	if len(f.inflowUI) > 0 {
		element[uiKey] = f.inflowUI
	}
	if f.rule != nil {
		element["rule"] = f.rule
	}

	// One message is written as an object rather than a one-element array: both
	// are accepted, and the common case should read as the single thing it is.
	switch len(f.notifs) {
	case 0:
	case 1:
		element[NotifKey] = f.notifs[0]
	default:
		element[NotifKey] = f.notifs
	}
	return element
}

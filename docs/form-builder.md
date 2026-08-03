# Form builder — action & settings UIs

Every action a plugin exposes can carry a **form**: the dialog a user fills in on
the canvas to configure that node. Settings (onboarding) forms work the same way.
Forms are declarative — the plugin ships JSON, the front end renders it.

## The `FormBuilder`

```go
type FormBuilder struct {
    SubmitTo   string `json:"submit_to"`  // optional meta function for live validation
    Jsonui     string `json:"jsonui"`     // the UI Schema (layout / widgets)
    Jsonschema string `json:"jsonschema"` // the JSON Schema (data model / validation)
}
```

- **`Jsonschema`** — a standard [JSON Schema](https://json-schema.org) describing
  the data your action expects. This defines fields, types, required-ness, and
  validation. It is exactly the shape that arrives back as the `body` of the
  request (see [protocol-inflowv1.md](protocol-inflowv1.md)).
- **`Jsonui`** — a **UI Schema** describing how to lay the fields out (groups,
  ordering, widgets, labels).
- **`SubmitTo`** — optionally, the name of a **meta function** to call for live
  validation as the user edits (see below).

## Rendering: JSON Forms + `x-inflow-ui`

Forms are rendered by [**JSON Forms**](https://jsonforms.io) — the schema + UI
schema pattern. Inflowenger ships a Vue 3 renderer set,
[`@inflowenger/plugin-form-builder`](https://github.com/Inflowenger/inflow-js/tree/master/packages/plugin-form-builder),
that extends JSON Forms with the `x-inflow-ui` key: a control or layout may carry
a **button** that runs a named **action**, which is how a form calls back into the
plugin while it is open. Controls without `x-inflow-ui` fall through to the
standard renderers untouched, and because it is plain JSON Schema + UI Schema, any
JSON Forms tooling can author or preview a form.

A minimal action form:

```go
schema := `{
  "type": "object",
  "properties": {
    "url":    { "type": "string", "title": "URL", "format": "uri" },
    "method": { "type": "string", "enum": ["GET", "POST", "PUT", "DELETE"] }
  },
  "required": ["url", "method"]
}`

ui := `{
  "type": "VerticalLayout",
  "elements": [
    { "type": "Control", "scope": "#/properties/url" },
    { "type": "Control", "scope": "#/properties/method" }
  ]
}`

p.AddAction(sdkv1.Action{
    Method:         "http.call",
    Title:          "HTTP Call",
    Description:    "Perform an outbound HTTP request",
    Icon:           sdkv1.Icon{Icon: "mdi-web"},
    Form:           sdkv1.FormBuilder{Jsonschema: schema, Jsonui: ui},
    RequestHandler: httpHandler,
})
```

The runtime fetches this form on demand from
`inflow.v1.<PLUGIN_ID>.http.call.@form`. What the user enters becomes the `body`
of the execution request.

## Settings (onboarding) forms

`RequiredParams` registers a plugin-level settings form plus a handler for when the
user submits it — useful for credentials or config the plugin needs before any
action runs:

```go
p.RequiredParams(&sdkv1.Settings{
    FormBuilder: sdkv1.FormBuilder{
        Jsonschema: settingsSchema,
        Jsonui:     settingsUi,
        SubmitTo:   "_settings.config.submit", // default if left blank
    },
    SubmitHandler: func(r sdkv1.Request) sdkv1.Response {
        // validate / persist the submitted settings
        return sdkv1.Response{Data: map[string]any{"ok": true}}
    },
})
```

The form is served on `inflow.v1.<PLUGIN_ID>.@settings`; submissions are handled on
`inflow.v1.<PLUGIN_ID>.<SubmitTo>` and answered with a `Response`.

`PluginIntro.Settings` is a related, lighter option: a `FormBuilder` attached
directly to the intro, usable as an onboarding stage shown when the plugin is first
added.

## Meta functions

A **meta function** is a lightweight request/reply handler (not a job) that the
front end can call while a form is open — to check that a URL is reachable, to
turn a typed name into the id an API needs, or to fill dependent fields. Register
them with `AddMeta` before `Start()`; each is served on
`inflow.v1.<PLUGIN_ID>.<Method>`:

```go
type Meta struct {
    Method         string
    RequestHandler func(sdkv1.Request) any
}

p.AddMeta(sdkv1.Meta{
    Method:         "my.meta.ping",
    RequestHandler: func(r sdkv1.Request) any { return sdkv1.Response{Data: …} },
})
```

The handler returns `any` and the SDK marshals it **verbatim** — a struct, a map,
or a bare array. It is not forced into the `{data, error}` envelope, and what it
should return depends on who is calling:

| Caller | Return |
|---|---|
| `FormBuilder.SubmitTo` — live validation of a form on submit | `sdkv1.Response` |
| An `x-inflow-ui` button on a control — filling fields in | the **patch object**, e.g. `map[string]any{"projectKey": "OPS"}` |

Unlike an action, a meta function is synchronous request/reply with no job,
progress, or context access.

> **Request shape.** A meta call made from a form arrives **flat** — the form's
> fields, plus `settings` and `value`, at the top level — *not* wrapped in the
> action's `{"_registry":…, "body":{…}}` envelope. `CastRequestTo` therefore
> returns a zero-valued struct here, with no error. Decode tolerantly.

Full contract, including the answer-shape rules and current limits:
[dependent-fields.md](https://github.com/Inflowenger/plugin-catalog/blob/main/docs/dependent-fields.md)
in the plugin catalog.

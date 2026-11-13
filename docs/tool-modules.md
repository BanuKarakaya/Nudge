# Compiled tool modules

Register trusted modules before serving requests with `SlackEventHandler.WithToolModule`.
Each module receives a server-authenticated `coding.Identity` and returns
`[]service.RegisteredTool`. Tool names must start with `ext_`; built-ins and duplicate
names cannot be shadowed. Definitions, input schema, callback and read-only status
are declared together. Omitted `ReadOnly` defaults to an external write.

```go
events.WithToolModule(func(identity coding.Identity) []service.RegisteredTool {
    return []service.RegisteredTool{{
        Definition: service.ToolDefinition{
            Name: "ext_ticket_lookup",
            Description: "Read a ticket the current user can access.",
            InputSchema: map[string]any{"type": "object", "properties": map[string]any{
                "id": map[string]any{"type": "string"},
            }, "required": []string{"id"}, "additionalProperties": false},
        },
        ReadOnly: true,
        Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
            // Parse and bound arguments. Look up credentials using identity.UserID,
            // verify identity.Team and live ticket access, then fetch the ticket.
            return lookupAuthorizedTicket(ctx, identity, raw)
        },
    }}
})
```

The sample callback is application-specific, not a supplied function. Registry
validation does not replace argument validation or provider authorization.
No callbacks run during registration. Identity must never come from model JSON.
Modules are reconstructed when executing an approved action, and the same policy
checks apply. Keep construction side-effect-free; the policy page may construct
modules with an empty identity to list their definitions.

This interface does not load arbitrary code, discover MCP servers, forward
workspace credentials to unknown hosts or provide a process sandbox.

package integration

// NewDefaultRegistry creates a registry pre-populated with all built-in
// integrations (webhook, github, etc.). App bootstrap code should call
// this once and pass the result into the API dependency graph.
func NewDefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(&WebhookIntegration{})
	r.Register(&GitHubIntegration{})
	r.Register(&DiscordIntegration{})
	r.Register(&JiraIntegration{})
	return r
}

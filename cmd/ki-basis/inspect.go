package main

import (
	"context"
	"encoding/json"
	"flag"
	"localaiserver/internal/agent"
	"localaiserver/internal/config"
	"os"
	"runtime"
	"runtime/debug"
)

func inspectCommand(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	path := flags.String("config", "config.local.json", "gateway configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	hash, err := agent.FileSHA256(context.Background(), binary)
	if err != nil {
		return err
	}
	build := map[string]string{}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.time" || setting.Key == "vcs.modified" {
				build[setting.Key] = setting.Value
			}
		}
	}
	models := []map[string]any{}
	for _, n := range c.Nodes {
		models = append(models, map[string]any{"id": n.ID, "model": n.Model, "revision": n.Revision, "model_sha256": n.ModelSHA256, "engine_sha256": n.EngineSHA256, "template_sha256": n.TemplateSHA256, "context_per_slot": n.ContextSize(), "slots": n.Capacity, "vision": n.Vision, "kind": n.Kind, "engine_protocol": n.EngineProtocol, "source": "declared_configuration"})
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "go_version": runtime.Version(), "binary_sha256": hash, "build": build, "generation_timeout_seconds": c.TimeoutSeconds, "minimum_timeout_seconds": c.MinimumTimeout(), "queue_wait_seconds": int(c.QueueWait().Seconds()), "queue_capacity": c.QueueSize(), "proxy_budget_seconds": int(c.ProxyBudget().Seconds()), "max_body_bytes": c.MaxBodyBytes, "max_concurrent_requests": c.ConcurrentLimit(), "api_key_count": len(c.Keys), "models": models, "deployment_configuration_valid": c.ValidateDeployment() == nil, "runtime_verified": false})
}

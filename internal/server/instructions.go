package server

import (
	"fmt"
	"strings"
)

// toolsetSummaries says what each optional toolset covers, for the server
// instructions. Every name knownToolsets returns needs one.
var toolsetSummaries = map[string]string{
	"alb":      "Application Load Balancers (httpproxies, routes, gateways, traffic protection)",
	"network":  "network services, the backends load balancers route to",
	"compute":  "workloads and their instances",
	"dns":      "managed DNS zones and records",
	"activity": "audit logs and events: who changed what, and what failed",
	"ipam":     "IP address classes, pools, claims and allocations",
	"vpc":      "Galactic VPC networks, subnets and connectors",
	"search":   "search for resources across the platform by kind, name and project",
	"iam":      "roles and policy bindings",
	"services": "services and service entitlements",
	"billing":  "billing accounts and invoices",
	"resource": "the generic 'resource' tool, for kinds without a dedicated tool",
}

// serverInstructions is the orientation a client gets at initialize: how to
// start, and which toolsets this session has loaded, can load, and can't.
// Without it an agent only learns a toolset exists by finding its tools, so
// one that isn't loaded is invisible and it would tell the user a task can't
// be done.
func serverInstructions(toolsets toolsetSelection) string {
	var on, available, off []string
	for _, name := range knownToolsets() {
		line := fmt.Sprintf("- %s: %s", name, toolsetSummaries[name])
		switch {
		case toolsets.on(name):
			if g := guideFor(name); g != "" {
				line += "; start with " + g
			}
			on = append(on, line)
		case toolsets.loadable(name):
			available = append(available, line)
		default:
			off = append(off, line)
		}
	}
	var b strings.Builder
	b.WriteString("Datum Cloud. Start with 'context' {}: it reports what's set up and its next_step says what to call next. " +
		"Project-scoped tools use DATUM_PROJECT, else the project set with 'projects' action=set, else a service account's own project.\n\n")
	b.WriteString("Toolsets loaded now:\n")
	if len(on) > 0 {
		b.WriteString(strings.Join(on, "\n") + "\n")
	} else {
		b.WriteString("- none beyond core\n")
	}
	if len(available) > 0 {
		b.WriteString("\nNot loaded yet, to keep the tool list small; each is one call away:\n" + strings.Join(available, "\n") + "\n" +
			"When a task needs one, load it with 'toolsets' action=load names=[...] and carry on: don't tell the user something " +
			"isn't possible before checking this list. If its tools don't appear afterwards, the client doesn't pick up tool " +
			"list changes: ask the user to add it to DATUM_MCP_ENABLE_TOOLSETS and restart the server.\n")
	}
	if len(off) > 0 {
		b.WriteString("\nTurned off by DATUM_MCP_DISABLE_TOOLSETS (can't be loaded from here):\n" + strings.Join(off, "\n") + "\n" +
			"If a task needs one, tell the user it's off rather than working around it.\n")
	}
	b.WriteString("\nThe <name>_guide tools say how each service works and how to diagnose it; where a guide says to hand " +
		"off to another service, use that service's toolset here, starting with its guide.")
	return b.String()
}

// handoffNote is appended to each agent toolset's preamble. The documents were
// written for the in-product assistant, where "hand off to Compute" means
// another agent; here it means another toolset on this same server.
func handoffNote(self string, toolsets toolsetSelection) string {
	var parts []string
	for _, t := range agentToolsets {
		if t.Name == self {
			continue
		}
		target := t.guideToolName() + ", then its tools"
		switch {
		case !toolsets.loadable(t.Name):
			target = "the " + t.Name + " toolset, which is turned off in this session: tell the user rather than diagnosing it yourself"
		case !toolsets.on(t.Name):
			target = t.guideToolName() + ", then its tools, after loading them with 'toolsets' action=load names=[\"" + t.Name + "\"] if they aren't listed"
		}
		parts = append(parts, fmt.Sprintf("%s -> %s", t.Subject, target))
	}
	return "\n- Hand-offs: where these documents say to hand off to another service, use its toolset on this server " +
		"instead, starting with its guide. " + strings.Join(parts, "; ") + "."
}

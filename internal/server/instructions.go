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
// start, and which toolsets this session has. Without it an agent only learns
// a toolset exists by finding its tools, so a turned-off one is invisible and
// it can't tell the user what to enable.
func serverInstructions(toolsets toolsetSelection) string {
	guides := map[string]string{}
	for _, t := range agentToolsets {
		guides[t.Name] = t.guideToolName()
	}
	var on, off []string
	for _, name := range knownToolsets() {
		line := fmt.Sprintf("- %s: %s", name, toolsetSummaries[name])
		if toolsets.on(name) {
			if g := guides[name]; g != "" {
				line += "; start with " + g
			}
			on = append(on, line)
		} else {
			off = append(off, line)
		}
	}
	var b strings.Builder
	b.WriteString("Datum Cloud. Start with 'context' {}: it reports what's set up and its next_step says what to call next. " +
		"Project-scoped tools use DATUM_PROJECT, else the project set with 'projects' action=set, else a service account's own project.\n\n")
	b.WriteString("Toolsets in this session:\n" + strings.Join(on, "\n") + "\n")
	if len(off) > 0 {
		b.WriteString("\nTurned off in this session (no tools here):\n" + strings.Join(off, "\n") + "\n" +
			"If a task needs one, tell the user it's off rather than working around it: they can turn it on with " +
			"DATUM_MCP_ENABLE_TOOLSETS / DATUM_MCP_DISABLE_TOOLSETS and restart the server.\n")
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
		if !toolsets.on(t.Name) {
			target = "the " + t.Name + " toolset, which is turned off in this session: tell the user rather than diagnosing it yourself"
		}
		parts = append(parts, fmt.Sprintf("%s -> %s", t.Subject, target))
	}
	return "\n- Hand-offs: where these documents say to hand off to another service, use its toolset on this server " +
		"instead, starting with its guide. " + strings.Join(parts, "; ") + "."
}

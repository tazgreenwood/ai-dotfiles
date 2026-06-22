package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// ── JSON-RPC types ─────────────────────────────────────────────────────────────

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── Tool schema types ──────────────────────────────────────────────────────────

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ── Wire ───────────────────────────────────────────────────────────────────────

func reply(id any, result any) {
	r := Response{JSONRPC: "2.0", ID: id, Result: result}
	b, _ := json.Marshal(r)
	fmt.Fprintf(os.Stdout, "%s\n", b)
}

func replyErr(id any, code int, msg string) {
	r := Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
	b, _ := json.Marshal(r)
	fmt.Fprintf(os.Stdout, "%s\n", b)
}

func toolOK(data any) ToolResult {
	b, _ := json.MarshalIndent(data, "", "  ")
	return ToolResult{Content: []ContentItem{{Type: "text", Text: string(b)}}}
}

func toolErr(msg string) ToolResult {
	return ToolResult{
		Content: []ContentItem{{Type: "text", Text: fmt.Sprintf(`{"error":%q}`, msg)}},
		IsError: true,
	}
}

// ── Dispatch ───────────────────────────────────────────────────────────────────

func handle(req Request) {
	switch req.Method {

	case "initialize":
		reply(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "registry", "version": "1.0.0"},
		})

	case "notifications/initialized", "ping":
		if req.ID != nil {
			reply(req.ID, map[string]any{})
		}

	case "tools/list":
		reply(req.ID, map[string]any{"tools": allTools()})

	case "tools/call":
		var p ToolCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			replyErr(req.ID, -32600, "invalid params: "+err.Error())
			return
		}
		result := dispatch(p.Name, p.Arguments)
		reply(req.ID, result)

	default:
		replyErr(req.ID, -32601, "method not found: "+req.Method)
	}
}

func dispatch(name string, args map[string]any) ToolResult {
	// Registry
	switch name {
	case "registry_get_project":
		return registryGetProject(args)
	case "registry_set":
		return registrySet(args)
	case "registry_init_project":
		return registryInitProject(args)
	case "registry_list_projects":
		return registryListProjects(args)
	case "registry_list_plans":
		return registryListPlans(args)
	case "registry_get_plan":
		return registryGetPlan(args)
	case "registry_write_plan":
		return registryWritePlan(args)
	case "registry_update_step":
		return registryUpdateStep(args)
	case "registry_write_audit":
		return registryWriteAudit(args)
	case "registry_get_audit":
		return registryGetAudit(args)
	case "registry_get_resources":
		return registryGetResources(args)
	case "registry_report_issue":
		return registryReportIssue(args)
	}
	return toolErr("unknown tool: " + name)
}

// ── Main ───────────────────────────────────────────────────────────────────────

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			replyErr(nil, -32700, "parse error: "+err.Error())
			continue
		}
		handle(req)
	}
}

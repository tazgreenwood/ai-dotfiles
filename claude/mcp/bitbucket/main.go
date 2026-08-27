package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

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

func allTools() []Tool {
	return bitbucketTools()
}

func dispatch(name string, args map[string]any) ToolResult {
	switch name {
	case "bitbucket_list_prs":
		return bbListPRs(args)
	case "bitbucket_get_pr":
		return bbGetPR(args)
	case "bitbucket_create_pr":
		return bbCreatePR(args)
	case "bitbucket_get_commits":
		return bbGetCommits(args)
	case "bitbucket_add_pr_comment":
		return bbAddPRComment(args)
	case "bitbucket_get_pr_comments":
		return bbGetPRComments(args)
	case "bitbucket_get_repo":
		return bbGetRepo(args)
	case "bitbucket_list_branches":
		return bbListBranches(args)
	case "bitbucket_get_diff":
		return bbGetDiff(args)
	}
	return toolErr("unknown tool: " + name)
}

func handle(req Request) {
	switch req.Method {
	case "initialize":
		reply(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "bitbucket", "version": "1.0.0"},
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

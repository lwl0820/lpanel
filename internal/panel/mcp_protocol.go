package panel

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"encoding/json/jsontext"
	"encoding/json/v2"
)

const (
	mcpLatestProtocol = "2025-11-25"
	mcpProtocolHeader = "MCP-Protocol-Version"
	mcpRequestLimit   = 7 << 20
)

type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Method  string         `json:"method"`
	Params  jsontext.Value `json:"params"`
}

type mcpResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Result  any            `json:"result,omitzero"`
	Error   *mcpRPCError   `json:"error,omitzero"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func mcpSupportsVersion(version string) bool {
	switch version {
	case mcpLatestProtocol, "2025-06-18":
		return true
	default:
		return false
	}
}

func (a *App) mcpOriginAllowed(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.User != nil || origin.Path != "" || origin.ForceQuery ||
		origin.RawQuery != "" || strings.Contains(origins[0], "#") || !strings.EqualFold(origin.Host, r.Host) {
		return false
	}
	host := origin.Hostname()
	ip := net.ParseIP(host)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	if net.ParseIP(peer).IsLoopback() && r.Header.Get("X-LPanel-Proxy") == "1" {
		config := a.currentConfig()
		publicPort := strconv.Itoa(config.PanelPort)
		if origin.Port() != publicPort && !(origin.Port() == "" &&
			(origin.Scheme == "http" && publicPort == "80" || origin.Scheme == "https" && publicPort == "443")) {
			return false
		}
		if config.PanelDomain != "" && !strings.EqualFold(strings.Trim(config.PanelDomain, "[]"), host) {
			return false
		}
		if config.PanelDomain == "" && ip == nil && !strings.EqualFold(host, "localhost") {
			return false
		}
		scheme = r.Header.Get("X-Forwarded-Proto")
		return (scheme == "http" || scheme == "https") && origin.Scheme == scheme
	}
	return origin.Scheme == scheme && (ip.IsLoopback() || strings.EqualFold(host, "localhost"))
}

func (a *App) serveMCPProtocol(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.mcpOriginAllowed(r) {
		WriteError(w, http.StatusForbidden, "invalid_origin", "MCP 请求来源无效")
		return
	}
	if version := r.Header.Get(mcpProtocolHeader); version != "" && !mcpSupportsVersion(version) {
		WriteError(w, http.StatusBadRequest, "unsupported_protocol_version", "不支持此 MCP 协议版本")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		WriteError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请使用 application/json 发送 MCP 请求")
		return
	}
	if !mcpAcceptsJSON(r.Header.Values("Accept")) {
		WriteError(w, http.StatusNotAcceptable, "invalid_accept", "客户端须接受 application/json 响应")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpRequestLimit))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			WriteError(w, http.StatusRequestEntityTooLarge, "request_too_large", "MCP 请求内容过大")
			return
		}
		WriteError(w, http.StatusBadRequest, "invalid_request", "无法读取 MCP 请求")
		return
	}
	response := mcpResponse{JSONRPC: "2.0", ID: jsontext.Value("null")}
	if !utf8.Valid(body) || !jsontext.Value(body).IsValid() {
		response.Error = &mcpRPCError{Code: -32700, Message: "JSON 格式错误"}
		WriteJSON(w, http.StatusBadRequest, response)
		return
	}
	var request mcpRequest
	if err := json.Unmarshal(body, &request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
		response.Error = &mcpRPCError{Code: -32600, Message: "无效的 JSON-RPC 请求"}
		WriteJSON(w, http.StatusBadRequest, response)
		return
	}
	if request.ID == nil {
		switch request.Method {
		case "notifications/initialized", "notifications/cancelled":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	switch request.ID.Kind() {
	case '"':
		response.ID = request.ID
	case '0':
		if !bytes.ContainsAny(request.ID, ".eE") {
			response.ID = request.ID
		}
	}
	if bytes.Equal(response.ID, []byte("null")) {
		response.Error = &mcpRPCError{Code: -32600, Message: "请求编号须为字符串或整数"}
		WriteJSON(w, http.StatusBadRequest, response)
		return
	}
	if request.Params != nil && request.Params.Kind() != '{' {
		response.Error = &mcpRPCError{Code: -32602, Message: "请求参数须为对象"}
		WriteJSON(w, http.StatusOK, response)
		return
	}
	response.Result, response.Error = a.dispatchMCP(r, request)
	WriteJSON(w, http.StatusOK, response)
}

func mcpAcceptsJSON(values []string) bool {
	if len(values) == 0 {
		return true
	}
	// 媒体范围的优先级从 JSON、application/* 到 */*。
	priority, quality := -1, 0.0
	for _, line := range values {
		for entry := range strings.SplitSeq(line, ",") {
			media, params, err := mime.ParseMediaType(entry)
			if err != nil {
				continue
			}
			rank := 0
			switch media {
			case "application/json":
				rank = 2
			case "application/*":
				rank = 1
			case "*/*":
			default:
				continue
			}
			if rank >= priority {
				q := 1.0
				if weight := params["q"]; weight != "" {
					q, _ = strconv.ParseFloat(weight, 64)
				}
				priority, quality = rank, q
			}
		}
	}
	return quality > 0
}

func (a *App) dispatchMCP(r *http.Request, request mcpRequest) (any, *mcpRPCError) {
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ProtocolVersion == "" {
			return nil, &mcpRPCError{Code: -32602, Message: "请提供客户端支持的 MCP 协议版本"}
		}
		version := params.ProtocolVersion
		if !mcpSupportsVersion(version) {
			version = mcpLatestProtocol
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "lpanel", "version": a.Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := a.mcpTools()
		definitions := make([]mcpToolDefinition, 0, len(tools))
		for _, tool := range tools {
			definitions = append(definitions, tool.Definition)
		}
		return map[string]any{"tools": definitions}, nil
	case "tools/call":
		return a.callMCPTool(r.Context(), request.Params)
	default:
		return nil, &mcpRPCError{Code: -32601, Message: "MCP 方法不存在"}
	}
}

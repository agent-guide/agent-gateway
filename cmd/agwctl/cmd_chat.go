package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	chatAPI       string
	chatBaseURL   string
	chatAPIKey    string
	chatModel     string
	chatSystem    string
	chatStream    bool
	chatMaxTokens int
	chatTimeout   float64
)

var chatCmd = &cobra.Command{
	Use:   "chat [prompt]",
	Short: "Send a chat request to the agent gateway LLM API",
	Long: `Send a single-turn chat request to the agent gateway's LLM API.

Supports OpenAI-compatible (/v1/chat/completions), Anthropic-compatible
(/v1/messages), and Claude Code CLI-compatible API surfaces, with optional SSE streaming.

Examples:
  agwctl chat "What is 2+2?"
  agwctl chat --api anthropic --model claude-sonnet-4-6 "Hello"
  agwctl chat --stream "Tell me a joke"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := defaultPrompt()
		if len(args) > 0 {
			prompt = args[0]
		}
		switch chatAPI {
		case "openai":
			return runChatOpenAI(prompt)
		case "anthropic", "cc":
			return runChatAnthropic(prompt)
		default:
			return fmt.Errorf("unknown --api %q: must be openai, anthropic, or cc", chatAPI)
		}
	},
}

// ── OpenAI chat/completions ───────────────────────────────────────────────────

func runChatOpenAI(prompt string) error {
	messages := []map[string]any{}
	if chatSystem != "" {
		messages = append(messages, map[string]any{"role": "system", "content": chatSystem})
	}
	messages = append(messages, map[string]any{"role": "user", "content": prompt})

	body := map[string]any{
		"messages":   messages,
		"max_tokens": chatMaxTokens,
		"stream":     chatStream,
	}
	if strings.TrimSpace(chatModel) != "" {
		body["model"] = chatModel
	}

	url := openAIEndpointURL(chatBaseURL, "/chat/completions")
	return doLLMRequest(url, body, func(resp *http.Response) error {
		if chatStream {
			return streamOpenAI(resp.Body)
		}
		return printOpenAIResponse(resp.Body)
	})
}

func printOpenAIResponse(r io.Reader) error {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return fmt.Errorf("no choices in response")
	}
	fmt.Println(resp.Choices[0].Message.Content)
	if resp.Usage != nil {
		fmt.Printf("usage: prompt_tokens=%d, completion_tokens=%d\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}
	return nil
}

func streamOpenAI(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[len("data: "):]
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			fmt.Print(chunk.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
	return scanner.Err()
}

// ── Anthropic messages ────────────────────────────────────────────────────────

func runChatAnthropic(prompt string) error {
	body := map[string]any{
		"max_tokens": chatMaxTokens,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
				},
			},
		},
		"stream": chatStream,
	}
	if strings.TrimSpace(chatModel) != "" {
		body["model"] = chatModel
	}
	if chatSystem != "" {
		body["system"] = chatSystem
	}

	url := anthropicEndpointURL(chatBaseURL, "/messages")
	return doLLMRequest(url, body, func(resp *http.Response) error {
		if chatStream {
			return streamAnthropic(resp.Body)
		}
		return printAnthropicResponse(resp.Body)
	})
}

func printAnthropicResponse(r io.Reader) error {
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	for _, block := range resp.Content {
		if block.Type == "text" {
			fmt.Println(block.Text)
		}
	}
	if resp.Usage != nil {
		fmt.Printf("usage: input_tokens=%d, output_tokens=%d\n",
			resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
	return nil
}

func streamAnthropic(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[len("data: "):]
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			fmt.Print(event.Delta.Text)
		}
	}
	fmt.Println()
	return scanner.Err()
}

// ── shared HTTP helper ────────────────────────────────────────────────────────

func doLLMRequest(url string, body map[string]any, handle func(*http.Response) error) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+chatAPIKey)
	req.Header.Set("x-api-key", chatAPIKey)

	// For streaming responses the timeout covers the full read duration; callers
	// should raise --timeout accordingly (or set 0 to disable).
	timeout := time.Duration(chatTimeout * float64(time.Second))
	client := &http.Client{Timeout: timeout}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return handle(resp)
}

func defaultPrompt() string {
	return envOr("AGW_PROMPT", "用一句中文回答：2 + 2 等于几？")
}

func defaultMaxTokens() int {
	if v := strings.TrimSpace(os.Getenv("AGW_MAX_TOKENS")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return 128
}

func defaultTimeoutSeconds() float64 {
	if v := strings.TrimSpace(os.Getenv("AGW_TIMEOUT")); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed
		}
	}
	return 30
}

func openAIEndpointURL(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + path
	}
	return base + "/v1" + path
}

func anthropicEndpointURL(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + path
	}
	return base + "/v1" + path
}

// ── init ──────────────────────────────────────────────────────────────────────

func init() {
	chatCmd.Flags().StringVar(&chatAPI, "api", envOr("AGW_API", "openai"),
		"LLM API surface: openai, anthropic, or cc")
	chatCmd.Flags().StringVar(&chatBaseURL, "base-url", envOr("AGW_BASE_URL", "http://127.0.0.1:8080/v1"),
		"gateway LLM API base URL (OpenAI usually includes /v1; Anthropic may omit it)")
	chatCmd.Flags().StringVar(&chatAPIKey, "api-key", envOr("AGW_API_KEY", "test-key"),
		"virtual key sent as Authorization: Bearer and x-api-key")
	chatCmd.Flags().StringVar(&chatModel, "model", envOr("AGW_MODEL", ""),
		"optional model name; leave empty to let the gateway route/provider default apply")
	chatCmd.Flags().StringVar(&chatSystem, "system", envOr("AGW_SYSTEM_PROMPT", ""),
		"optional system prompt")
	chatCmd.Flags().BoolVar(&chatStream, "stream", false,
		"use SSE streaming response")
	chatCmd.Flags().IntVar(&chatMaxTokens, "max-tokens", defaultMaxTokens(),
		"maximum output tokens")
	chatCmd.Flags().Float64Var(&chatTimeout, "timeout", defaultTimeoutSeconds(),
		"HTTP request timeout in seconds (0 = no timeout)")

	rootCmd.AddCommand(chatCmd)
}

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

var responsesCmd = &cobra.Command{
	Use:   "responses [prompt]",
	Short: "Send an OpenAI Responses API request to the agent gateway",
	Long: `Send a single-turn request to the agent gateway's OpenAI-compatible
Responses API (/v1/responses), with optional SSE streaming.

Examples:
  agwctl responses "What is 2+2?"
  agwctl responses --model gpt-4.1 "Summarize this"
  agwctl responses --stream "Tell me a joke"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prompt := defaultPrompt()
		if len(args) > 0 {
			prompt = args[0]
		}
		return runResponses(prompt)
	},
}

func runResponses(prompt string) error {
	input := []map[string]any{}
	if chatSystem != "" {
		input = append(input, map[string]any{"role": "system", "content": chatSystem})
	}
	input = append(input, map[string]any{"role": "user", "content": prompt})

	body := map[string]any{
		"input":             input,
		"max_output_tokens": chatMaxTokens,
		"stream":            chatStream,
	}
	if strings.TrimSpace(chatModel) != "" {
		body["model"] = chatModel
	}

	url := openAIEndpointURL(chatBaseURL, "/responses")
	return doLLMRequest(url, body, func(resp *http.Response) error {
		if chatStream {
			return streamResponses(resp.Body)
		}
		return printResponsesResponse(resp.Body)
	})
}

func printResponsesResponse(r io.Reader) error {
	var resp struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if strings.TrimSpace(resp.OutputText) != "" {
		fmt.Println(resp.OutputText)
	} else {
		printed := false
		for _, item := range resp.Output {
			if item.Type != "message" {
				continue
			}
			for _, part := range item.Content {
				if part.Text == "" {
					continue
				}
				fmt.Println(part.Text)
				printed = true
			}
		}
		if !printed {
			return fmt.Errorf("no output text in response")
		}
	}
	if resp.Usage != nil {
		fmt.Printf("usage: input_tokens=%d, output_tokens=%d, total_tokens=%d\n",
			resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens)
	}
	return nil
}

func streamResponses(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[len("data: "):]
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if event.Type == "response.output_text.delta" {
			fmt.Print(event.Delta)
		}
	}
	fmt.Println()
	return scanner.Err()
}

func init() {
	responsesCmd.Flags().StringVar(&chatBaseURL, "base-url", envOr("AGW_BASE_URL", "http://127.0.0.1:8080/v1"),
		"gateway OpenAI Responses API base URL (usually includes /v1)")
	responsesCmd.Flags().StringVar(&chatAPIKey, "api-key", envOr("AGW_API_KEY", "test-key"),
		"virtual key sent as Authorization: Bearer and x-api-key")
	responsesCmd.Flags().StringVar(&chatModel, "model", envOr("AGW_MODEL", ""),
		"optional model name; leave empty to let the gateway route/provider default apply")
	responsesCmd.Flags().StringVar(&chatSystem, "system", envOr("AGW_SYSTEM_PROMPT", ""),
		"optional system instructions")
	responsesCmd.Flags().BoolVar(&chatStream, "stream", false,
		"use SSE streaming response")
	responsesCmd.Flags().IntVar(&chatMaxTokens, "max-output-tokens", defaultMaxTokens(),
		"maximum output tokens")
	responsesCmd.Flags().Float64Var(&chatTimeout, "timeout", defaultTimeoutSeconds(),
		"HTTP request timeout in seconds (0 = no timeout)")

	rootCmd.AddCommand(responsesCmd)
}

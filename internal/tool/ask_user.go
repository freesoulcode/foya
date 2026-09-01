package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/freesoulcode/foya/internal/question"
)

type askUserTool struct {
	gateway question.Gateway
}

type askUserParams struct {
	Questions []question.Question `json:"questions"`
}

// NewAskUserTool lets the agent collect a complete batch of user decisions
// without ending the current turn.
func NewAskUserTool(gateway question.Gateway) Tool {
	return &askUserTool{gateway: gateway}
}

func (t *askUserTool) Name() string       { return "ask_user" }
func (t *askUserTool) Exposure() Exposure { return ExposureDirect }
func (t *askUserTool) Description() string {
	return "Ask the user one or more focused questions when their answer is needed to continue. Ask up to 8 questions at once. The tool waits until the user completes every answer; do not use it for information you can discover with available tools."
}

func (t *askUserTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"questions":{
				"type":"array",
				"minItems":1,
				"maxItems":8,
				"items":{
					"type":"object",
					"properties":{
						"id":{"type":"string","description":"Stable question ID unique within this call"},
						"question":{"type":"string","description":"The question shown to the user"},
						"description":{"type":"string","description":"Optional context that helps the user decide"},
						"options":{
							"type":"array",
							"maxItems":8,
							"items":{
								"type":"object",
								"properties":{
									"label":{"type":"string"},
									"description":{"type":"string"},
									"recommended":{"type":"boolean"}
								},
								"required":["label"],
								"additionalProperties":false
							}
						},
						"allow_custom":{"type":"boolean","description":"Allow free-text input in addition to options"}
					},
					"required":["question"],
					"additionalProperties":false
				}
			}
		},
		"required":["questions"],
		"additionalProperties":false
	}`)
}

func (t *askUserTool) Run(ctx context.Context, call Call) (Result, error) {
	var params askUserParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	sessionID := SessionIDFromContext(ctx)
	if strings.TrimSpace(sessionID) == "" {
		return errResult("ask_user requires a session"), nil
	}
	answers, err := t.gateway.Ask(ctx, question.Batch{
		SessionID:  sessionID,
		RunID:      RunIDFromContext(ctx),
		ToolCallID: call.ID,
		Questions:  params.Questions,
	})
	if err != nil {
		if errors.Is(err, question.ErrCancelled) || errors.Is(err, context.Canceled) {
			return errResult("user question batch was cancelled"), nil
		}
		return errResult("ask_user failed: " + err.Error()), nil
	}
	data, err := json.Marshal(struct {
		Answers []question.Answer `json:"answers"`
	}{Answers: answers})
	if err != nil {
		return errResult("encode answers: " + err.Error()), nil
	}
	return textResult(string(data)), nil
}

package feishu

import (
	"context"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel"
	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type Channel interface {
	types.Channel
	React(context.Context, string, string) error
}

type officialChannel struct {
	types.Channel
	client *lark.Client
}

func NewChannel(appID, appSecret string) Channel {
	handler := dispatcher.NewEventDispatcher("", "")
	client := lark.NewClient(
		appID,
		appSecret,
		lark.WithLogLevel(larkcore.LogLevelWarn),
		lark.WithSource("foya"),
	)
	wsClient := larkws.NewClient(
		appID,
		appSecret,
		larkws.WithEventHandler(handler),
		larkws.WithLogLevel(larkcore.LogLevelWarn),
	)
	return &officialChannel{
		Channel: channel.NewChannel(client, wsClient),
		client:  client,
	}
}

func (c *officialChannel) React(
	ctx context.Context,
	messageID string,
	emojiType string,
) error {
	request := larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(emojiType).Build()).
			Build()).
		Build()
	response, err := c.client.Im.V1.MessageReaction.Create(ctx, request)
	if err != nil {
		return err
	}
	if !response.Success() {
		return fmt.Errorf("Feishu reaction failed: code=%d message=%s", response.Code, response.Msg)
	}
	return nil
}

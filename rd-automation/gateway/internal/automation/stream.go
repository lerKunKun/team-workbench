package automation

import (
	"context"
	"errors"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
)

const groupSpace = "dtv1.card//IM_GROUP."

func (s *Server) Stream(ctx context.Context) {
	if s.cfg.Demo || !s.cfg.Stream || (s.cfg.WorkbenchMode && !s.cfg.workbenchReady()) || (!s.cfg.WorkbenchMode && !s.cfg.V2Ready()) {
		log.Print("DingTalk Stream disabled (feature off, demo, or missing V2 settings)")
		return
	}
	// Keep the SDK's default no-op logger: some of its logs include session tickets.
	c := client.NewStreamClient(client.WithAppCredential(client.NewAppCredentialConfig(s.cfg.DingID, s.cfg.DingSecret)), client.WithAutoReconnect(true))
	c.RegisterChatBotCallbackRouter(s.onMessage)
	c.RegisterCardCallbackRouter(s.onCard)
	for ctx.Err() == nil {
		start, stop := context.WithTimeout(ctx, 30*time.Second)
		err := c.Start(start)
		stop()
		if err == nil {
			log.Print("DingTalk Stream initially connected")
			<-ctx.Done()
			// The SDK reconnect worker has no stop context; process exit terminates it.
			return
		}
		log.Print("DingTalk Stream initial connection failed; retry in 30s")
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

func (s *Server) onMessage(ctx context.Context, d *chatbot.BotCallbackDataModel) ([]byte, error) {
	if s.cfg.WorkbenchMode {
		if !s.cfg.workbenchMessageAllowed(d.ConversationType, d.SenderCorpId, d.SenderStaffId) || d.ChatbotCorpId != s.cfg.WorkbenchCorp || d.MsgId == "" {
			return nil, nil
		}
		text := compact(d.Text.Content, 2500)
		if text == "" {
			return nil, nil
		}
		ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		return nil, s.st.event(ctx, "dingtalk-workbench", d.MsgId, "private_message", "intake", Payload{Corp: d.SenderCorpId, Sender: d.SenderStaffId, Text: text})
	}
	if !s.cfg.Groups[d.ConversationId] || d.ConversationType != "2" || d.MsgId == "" || d.SenderStaffId == "" {
		return nil, nil
	}
	text := compact(d.Text.Content, 2500)
	if text == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if _, err := s.st.member(ctx, d.SenderStaffId); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return nil, s.st.event(ctx, "dingtalk", d.MsgId, "chatbot_message", "intake", Payload{Group: d.ConversationId, Sender: d.SenderStaffId, Text: text})
}

func (s *Server) onCard(ctx context.Context, r *card.CardRequest) (*card.CardResponse, error) {
	if s.cfg.WorkbenchMode {
		return s.onWorkbenchCard(ctx, r)
	}
	res := &card.CardResponse{}
	group := strings.TrimPrefix(r.SpaceId, groupSpace)
	// DingTalk can omit SpaceId on an action; the unique stored track ID still
	// binds the action to the card's original allowlisted group.
	if (group != "" && !s.cfg.Groups[group]) || r.UserId == "" || (r.UserIdType != 0 && r.UserIdType != 1) || (r.Type != "" && r.Type != "actionCallback") {
		return res, nil
	}
	id, action := r.GetActionString("draft_id"), r.GetActionString("action")
	if !validID(id) || r.OutTrackId != "draft_"+id {
		return res, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	_, err := s.st.claim(ctx, id, r.OutTrackId, group, r.UserId, action, false, slices.Collect(maps.Keys(s.cfg.Groups)))
	// Let only the worker modify the shared card, so the callback's ACK cannot
	// overwrite a final result if processing finishes quickly.
	return res, err
}

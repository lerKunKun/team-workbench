package automation

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
	"net/url"
	"strings"
	"time"
)

const privateSpace = "dtv1.card//IM_ROBOT."
const privateTrack = "wb_"

func (c Config) workbenchReady() bool {
	u, e := url.Parse(c.WorkbenchAPI)
	return c.DingReady() && c.Template != "" && c.WorkbenchCorp != "" && len(c.WorkbenchToken) >= 32 && e == nil && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" && (u.Scheme == "https" || (u.Scheme == "http" && u.Hostname() == "team-workbench-api"))
}
func (c Config) workbenchMessageAllowed(kind, corp, staff string) bool {
	return c.WorkbenchMode && kind == "1" && c.WorkbenchCorp != "" && corp == c.WorkbenchCorp && staff != ""
}
func directCardSpace(space, kind, staff string) bool {
	return (kind == "" || strings.EqualFold(kind, "IM_ROBOT")) && (space == "" || space == staff || space == privateSpace+staff)
}
func (s *Server) onWorkbenchCard(ctx context.Context, r *card.CardRequest) (*card.CardResponse, error) {
	res := &card.CardResponse{}
	if r.CorpId != s.cfg.WorkbenchCorp || r.UserId == "" || (r.UserIdType != 0 && r.UserIdType != 1) || (r.Type != "" && r.Type != "actionCallback") || !directCardSpace(r.SpaceId, r.SpaceType, r.UserId) {
		return res, nil
	}
	id, action := r.GetActionString("draft_id"), r.GetActionString("action")
	if !validID(id) || r.OutTrackId != privateTrack+id || (action != "confirm" && action != "cancel") {
		return res, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	tx, e := s.st.pool.Begin(ctx)
	if e != nil {
		return res, e
	}
	defer tx.Rollback(ctx)
	status := "processing"
	if action == "cancel" {
		status = "cancelled"
	}
	tag, e := tx.Exec(ctx, `UPDATE app.workbench_private_card SET state=$4 WHERE id=$1 AND corp=$2 AND staff=$3 AND delivered AND state='pending' AND created_at>now()-interval '30 minutes'`, id, r.CorpId, r.UserId, status)
	if e != nil || tag.RowsAffected() != 1 {
		return res, e
	}
	if e = enqueue(ctx, tx, "action", Payload{Corp: r.CorpId, Sender: r.UserId, DraftID: id, Action: action}); e != nil {
		return res, e
	}
	return res, tx.Commit(ctx)
}
func (s *Server) workbenchCall(ctx context.Context, route string, j Job, draft string, out any) error {
	if !s.cfg.WorkbenchMode || !s.cfg.workbenchReady() || j.Payload.Corp != s.cfg.WorkbenchCorp || j.Payload.Sender == "" {
		return fmt.Errorf("workbench private mode disabled")
	}
	return call(ctx, hookClient, "POST", s.cfg.WorkbenchAPI+"/internal/bot/"+route, "workbench", "X-Workbench-Bot-Token", s.cfg.WorkbenchToken, map[string]string{"channel": "1", "corp": j.Payload.Corp, "staff": j.Payload.Sender, "text": j.Payload.Text, "draftId": draft}, out)
}
func (s *Server) workbenchIntake(ctx context.Context, j Job) error {
	if j.Payload.Corp == "" {
		return fmt.Errorf("non-private job cannot use workbench")
	}
	var delivered bool
	e := s.st.pool.QueryRow(ctx, `SELECT delivered FROM app.workbench_private_card WHERE id=$1`, j.ID).Scan(&delivered)
	if e == nil && delivered {
		return nil
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	var msg struct {
		Type     string
		Summary  string
		Markdown string `json:"tasks_md"`
		Status   string `json:"status_text"`
	}
	if err := s.workbenchCall(ctx, "intake", j, j.ID, &msg); err != nil {
		return err
	}
	if msg.Type != "help" && msg.Type != "draft" {
		return fmt.Errorf("workbench response invalid")
	}
	state, hide := "pending", "false"
	if msg.Type == "help" {
		state, hide = "finished", "true"
	}
	if _, e = s.st.pool.Exec(ctx, `INSERT INTO app.workbench_private_card(id,corp,staff,state) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, j.ID, j.Payload.Corp, j.Payload.Sender, state); e != nil {
		return e
	}
	if e = s.ding.sendPrivateCard(ctx, j.Payload.Sender, j.ID, map[string]string{"draft_id": j.ID, "summary": msg.Summary, "preview_md": msg.Markdown, "status_text": msg.Status, "is_confirmed": hide}); e != nil {
		return e
	}
	_, e = s.st.pool.Exec(ctx, `UPDATE app.workbench_private_card SET delivered=true WHERE id=$1`, j.ID)
	return e
}
func (s *Server) workbenchAction(ctx context.Context, j Job) error {
	if !s.cfg.WorkbenchMode || !s.cfg.workbenchReady() || j.Payload.Corp != s.cfg.WorkbenchCorp {
		return fmt.Errorf("workbench private mode disabled")
	}
	var state string
	if e := s.st.pool.QueryRow(ctx, `SELECT state FROM app.workbench_private_card WHERE id=$1 AND corp=$2 AND staff=$3`, j.Payload.DraftID, j.Payload.Corp, j.Payload.Sender).Scan(&state); e != nil {
		return e
	}
	if state == "finished" {
		return nil
	}
	text := "已取消，不会写入。"
	if j.Payload.Action == "confirm" && state == "processing" {
		var r struct {
			Identifier, URL, Status string
			Demo                    bool
		}
		if e := s.workbenchCall(ctx, "execute", j, j.Payload.DraftID, &r); e != nil {
			text = "没有确认写入成功，请到工作台核对，勿重复提交。"
		} else {
			text = "已记录。"
			if r.Status == "pending_approval" {
				text = "立项申请已进入待审批。"
			}
			if r.Demo {
				text = "演示 · " + text
			}
			if linearLink(r.URL) {
				text += " [查看 Linear](" + r.URL + ")"
			}
			if r.Identifier != "" {
				text += " " + inline(r.Identifier)
			}
		}
	} else if j.Payload.Action != "cancel" || state != "cancelled" {
		return fmt.Errorf("private card state mismatch")
	}
	if e := s.ding.updateCard(ctx, Draft{TrackID: privateTrack + j.Payload.DraftID}, text); e != nil {
		return e
	}
	_, e := s.st.pool.Exec(ctx, `UPDATE app.workbench_private_card SET state='finished' WHERE id=$1 AND corp=$2 AND staff=$3`, j.Payload.DraftID, j.Payload.Corp, j.Payload.Sender)
	return e
}
func (d *Ding) sendPrivateCard(ctx context.Context, staff, id string, params map[string]string) error {
	var res struct {
		Success bool
		Result  struct {
			OutTrackId     string
			DeliverResults []struct {
				Success bool
				SpaceId string
			}
		}
	}
	track := privateTrack + id
	e := d.api(ctx, "POST", "/v1.0/card/instances/createAndDeliver", map[string]any{"cardTemplateId": d.c.Template, "outTrackId": track, "callbackType": "STREAM", "userIdType": 1, "openSpaceId": privateSpace + staff,
		"cardData": map[string]any{"cardParamMap": params}, "imRobotOpenSpaceModel": map[string]bool{"supportForward": false}, "imRobotOpenDeliverModel": map[string]string{"robotCode": d.c.Robot, "spaceType": "IM_ROBOT"}}, &res)
	if e != nil {
		return e
	}
	if res.Success && res.Result.OutTrackId == track {
		for _, r := range res.Result.DeliverResults {
			if r.Success && (r.SpaceId == staff || r.SpaceId == privateSpace+staff) {
				return nil
			}
		}
	}
	return fmt.Errorf("dingtalk API private card delivery not confirmed")
}

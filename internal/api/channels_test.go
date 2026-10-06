package api_test

import (
	"context"
	"github.com/tung1998/agent-office/internal/storage"
	"strings"
	"testing"
)

// ADR-048: channels are set up per project; the bot token is written, never read back.
func TestChannelsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/channels", map[string]any{"kind": "slack", "name": "x", "token": "t"}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown kind = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/channels", map[string]any{"kind": "telegram", "name": "x", "allow": []string{"*"}}, nil); resp.StatusCode != 400 {
		t.Fatalf("no token = %d", resp.StatusCode)
	}
	// review C2: an enabled channel names who may use it ("*" = anyone, on purpose)
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/channels", map[string]any{"kind": "telegram", "name": "x", "token": "t"}, nil); resp.StatusCode != 400 {
		t.Fatalf("an open channel without an allow list = %d", resp.StatusCode)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/channels", map[string]any{
		"kind": "telegram", "name": "Hỗ trợ", "token": "123:secret-bot-token", "scope": "đơn hàng", "filter_enabled": true, "allow": []string{"42"}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	ch := body["channel"].(map[string]any)
	id := ch["id"].(string)
	if ch["has_token"] != true || strings.Contains(mustJSON(body), "secret-bot-token") || ch["mode"] != "read" {
		t.Fatalf("channel = %v", ch)
	}
	stored, _ := e.st.Channels().Get(context.Background(), id)
	if stored.TokenEnc == "" || strings.Contains(stored.TokenEnc, "secret-bot-token") {
		t.Fatalf("token stored in clear: %q", stored.TokenEnc)
	}
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/channels/"+id, map[string]any{"name": "Hỗ trợ 2", "enabled": false}, nil)
	if resp.StatusCode != 200 || body["channel"].(map[string]any)["name"] != "Hỗ trợ 2" {
		t.Fatalf("patch = %d %v", resp.StatusCode, body)
	}
	if again, _ := e.st.Channels().Get(context.Background(), id); again.TokenEnc != stored.TokenEnc {
		t.Fatal("a patch without token changed the token")
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/channels", nil, nil)
	if len(body["channels"].([]any)) != 1 {
		t.Fatalf("list = %v", body)
	}
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "GET", e.srv.URL+"/api/projects/"+pid+"/channels", nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member list = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "DELETE", e.srv.URL+"/api/channels/"+id, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
}

// ADR-049: a channel's messages are handled by automations whose source is
// the channel; each names the channel, keywords and a scope.
func TestChannelAutomationAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/channels", map[string]any{"kind": "telegram", "name": "Hỗ trợ", "token": "1:t", "allow": []string{"*"}}, nil)
	chID := body["channel"].(map[string]any)["id"].(string)
	create := func(source string, cfg map[string]any) (int, map[string]any) {
		resp, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": "Tra đơn", "source": source, "action": "chat", "config": cfg}, nil)
		return resp.StatusCode, b
	}
	if code, b := create("telegram", map[string]any{}); code != 400 {
		t.Fatalf("no channel = %d %v", code, b)
	}
	if code, b := create("discord", map[string]any{"channel_id": chID}); code != 400 {
		t.Fatalf("a telegram channel for a discord source = %d %v", code, b)
	}
	code, b := create("telegram", map[string]any{"channel_id": chID, "keywords": []string{" đơn ", ""}, "scope": "đơn hàng"})
	if code != 201 {
		t.Fatalf("create = %d %v", code, b)
	}
	cfg := b["automation"].(map[string]any)["config"].(map[string]any)
	if cfg["channel_id"] != chID || cfg["scope"] != "đơn hàng" || mustJSON(cfg["keywords"]) != `["đơn"]` {
		t.Fatalf("config = %v", cfg)
	}
	if _, has := b["secret"]; has && b["secret"] != "" {
		t.Fatalf("a channel automation got a webhook secret: %v", b["secret"])
	}
}

// A bot is the trigger's own settings: saved with the automation, shared by
// the automations that name it, gone with the last of them.
func TestAutomationCarriesItsBot(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	post := func(b map[string]any) (int, map[string]any) {
		resp, out := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", b, nil)
		return resp.StatusCode, out
	}
	// no one allowed: refused, and no bot left behind
	if code, b := post(map[string]any{"name": "Trả lời", "source": "discord", "action": "chat", "bot": map[string]any{"token": "tok-1"}}); code != 400 {
		t.Fatalf("no allow list = %d %v", code, b)
	}
	if list, _ := e.st.Channels().List(ctx, pid); len(list) != 0 {
		t.Fatalf("a failed save left a bot: %+v", list)
	}
	// a bot made then dropped (the automation refused) leaves no trace in the log
	if code, b := post(map[string]any{"name": "Trả lời", "source": "discord", "action": "chat", "model_tier": "bogus",
		"bot": map[string]any{"token": "tok-2", "allow": []string{"42"}}}); code != 400 {
		t.Fatalf("a bad automation = %d %v", code, b)
	}
	if list, _ := e.st.Channels().List(ctx, pid); len(list) != 0 {
		t.Fatalf("a refused automation left a bot: %+v", list)
	}
	if es, _ := e.st.Audit().List(ctx, storage.AuditFilter{Resource: "channel"}); len(es) != 0 {
		t.Fatalf("a dropped bot was logged: %+v", es)
	}
	code, b := post(map[string]any{"name": "Trả lời", "source": "discord", "action": "chat",
		"bot": map[string]any{"token": "secret-discord-token", "allow": []string{"42"}, "refusal": "Chỉ hỗ trợ đơn hàng"}})
	if code != 201 || strings.Contains(mustJSON(b), "secret-discord-token") {
		t.Fatalf("create = %d %v", code, b)
	}
	a := b["automation"].(map[string]any)
	bot := a["bot"].(map[string]any)
	chID := a["config"].(map[string]any)["channel_id"].(string)
	if chID == "" || bot["has_token"] != true || mustJSON(bot["allow"]) != `["42"]` || bot["refusal"] != "Chỉ hỗ trợ đơn hàng" {
		t.Fatalf("automation = %v", a)
	}
	ch, err := e.st.Channels().Get(ctx, chID)
	if err != nil || ch.Kind != "discord" || !ch.Enabled || ch.TokenEnc == "" || strings.Contains(ch.TokenEnc, "secret") {
		t.Fatalf("bot = %+v %v", ch, err)
	}
	// edit the bot from the automation; the token stays
	id := a["id"].(string)
	resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/automations/"+id, map[string]any{"name": "Trả lời", "source": "discord", "action": "chat",
		"config": map[string]any{"channel_id": chID}, "bot": map[string]any{"allow": []string{"42", "43"}}}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("patch = %d %v", resp.StatusCode, b)
	}
	if again, _ := e.st.Channels().Get(ctx, chID); len(again.Allow) != 2 || again.TokenEnc != ch.TokenEnc {
		t.Fatalf("bot after patch = %+v", again)
	}
	// a second rule on the same bot; the bot goes with the last one
	code, b = post(map[string]any{"name": "Tra mã", "source": "discord", "action": "chat", "config": map[string]any{"channel_id": chID, "keywords": []string{"mã"}}})
	if code != 201 || b["automation"].(map[string]any)["bot_status"].(map[string]any)["shared"] != float64(2) {
		t.Fatalf("second = %d %v", code, b)
	}
	second := b["automation"].(map[string]any)["id"].(string)
	do(t, admin, "DELETE", e.srv.URL+"/api/automations/"+id, nil, nil)
	if _, err := e.st.Channels().Get(ctx, chID); err != nil {
		t.Fatal("the bot went while a rule still uses it")
	}
	do(t, admin, "DELETE", e.srv.URL+"/api/automations/"+second, nil, nil)
	if _, err := e.st.Channels().Get(ctx, chID); err == nil {
		t.Fatal("the bot stayed after its last rule")
	}
}

// A bot's automation runs on a message: "run now" has nothing to run.
func TestChannelAutomationDoesNotRunByHand(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": "Trả lời", "source": "discord", "action": "chat",
		"bot": map[string]any{"token": "tok", "allow": []string{"42"}}}, nil)
	id := b["automation"].(map[string]any)["id"].(string)
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/automations/"+id+"/run", map[string]any{}, nil); resp.StatusCode != 400 {
		t.Fatalf("run = %d %v", resp.StatusCode, b)
	}
}

// A custom command is an automation of the bot: its name made safe for the
// "/" menus, unique on the bot, not one of the bot's own.
func TestChannelCustomCommandAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	create := func(cfg map[string]any, bot map[string]any) (int, map[string]any) {
		b := map[string]any{"name": "Tra đơn", "source": "discord", "action": "script", "script": map[string]any{"lang": "bash", "body": "cat"}, "config": cfg}
		if bot != nil {
			b["bot"] = bot
		}
		resp, out := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", b, nil)
		return resp.StatusCode, out
	}
	code, b := create(map[string]any{"command": "/Đơn Hàng", "command_description": "Tra trạng thái đơn", "command_arg": "mã đơn", "keywords": []string{"x"}}, map[string]any{"token": "tok", "allow": []string{"*"}})
	if code != 201 {
		t.Fatalf("create = %d %v", code, b)
	}
	cfg := b["automation"].(map[string]any)["config"].(map[string]any)
	if cfg["command"] != "don-hang" || cfg["command_description"] != "Tra trạng thái đơn" || cfg["command_arg"] != "mã đơn" || mustJSON(cfg["keywords"]) != `[]` {
		t.Fatalf("config = %v", cfg)
	}
	chID := cfg["channel_id"].(string)
	if code, b := create(map[string]any{"channel_id": chID, "command": "don-hang"}, nil); code != 400 {
		t.Fatalf("a second /don-hang = %d %v", code, b)
	}
	if code, b := create(map[string]any{"channel_id": chID, "command": "job"}, nil); code != 400 {
		t.Fatalf("/job taken = %d %v", code, b)
	}
}

// Review I5: a refused automation change changes nothing of its bot either.
func TestRefusedAutomationLeavesItsBot(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": "Trả lời", "source": "discord", "action": "chat",
		"bot": map[string]any{"token": "tok", "allow": []string{"42"}}}, nil)
	a := b["automation"].(map[string]any)
	chID := a["config"].(map[string]any)["channel_id"].(string)
	resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/automations/"+a["id"].(string), map[string]any{"name": "Trả lời", "source": "discord", "action": "chat",
		"config": map[string]any{"channel_id": chID, "command": "job"}, "bot": map[string]any{"allow": []string{"*"}}}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("a reserved command = %d", resp.StatusCode)
	}
	if ch, _ := e.st.Channels().Get(ctx, chID); len(ch.Allow) != 1 || ch.Allow[0] != "42" {
		t.Fatalf("the refused change opened the bot: %v", ch.Allow)
	}
}

// The line on top of a bot's answers is set with the bot and read back.
func TestChannelHeaderAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	ch, _ := e.st.Channels().Create(context.Background(), storage.Channel{ProjectID: pid, Kind: "discord", Name: "Dev", TokenEnc: "enc"})
	resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/channels/"+ch.ID, map[string]any{"header": " {agent} · {branch} "}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("patch = %d %v", resp.StatusCode, b)
	}
	got, _ := e.st.Channels().Get(context.Background(), ch.ID)
	if got.Header != "{agent} · {branch}" {
		t.Fatalf("header = %q", got.Header)
	}
	_, b = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/channels", nil, nil)
	if c := b["channels"].([]any)[0].(map[string]any); c["header"] != "{agent} · {branch}" {
		t.Fatalf("list = %v", c)
	}
}

// A bot's reply mode and its commands' default setup are set with the bot and
// read back; a bad one is refused.
func TestChannelDefaultsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	ch, _ := e.st.Channels().Create(context.Background(), storage.Channel{ProjectID: pid, Kind: "discord", Name: "Dev", TokenEnc: "enc"})
	url := e.srv.URL + "/api/channels/" + ch.ID
	defaults := map[string]any{"action": "chat", "agent_id": "", "config": map[string]any{"tags": []string{"bot"}}}
	if resp, b := do(t, admin, "PATCH", url, map[string]any{"reply_mode": "steps", "defaults": defaults}, nil); resp.StatusCode != 200 {
		t.Fatalf("patch = %d %v", resp.StatusCode, b)
	}
	got, _ := e.st.Channels().Get(context.Background(), ch.ID)
	if got.ReplyMode != storage.ReplySteps || got.Defaults != `{"action":"chat","agent_id":"","config":{"tags":["bot"]}}` {
		t.Fatalf("saved = %q %q", got.ReplyMode, got.Defaults)
	}
	_, b := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/channels", nil, nil)
	c := b["channels"].([]any)[0].(map[string]any)
	if d, _ := c["defaults"].(map[string]any); c["reply_mode"] != "steps" || d["action"] != "chat" {
		t.Fatalf("list = %v", c)
	}
	for _, bad := range []map[string]any{{"reply_mode": "loud"}, {"defaults": []int{1}}} {
		if resp, _ := do(t, admin, "PATCH", url, bad, nil); resp.StatusCode != 400 {
			t.Errorf("%v = %d, want 400", bad, resp.StatusCode)
		}
	}
}

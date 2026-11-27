package session

import (
	"encoding/json"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/acp/session/acpupdate"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

// sessionMetaCache keeps the latest structured session state pushed by the
// agent (config options, slash commands, title, mode, usage) so a turn that
// joins an already-pooled instance can be told the current state up front,
// and so operators can inspect pooled sessions through the runtime Admin API.
// Each entry stores the raw ACP update object of its kind.
type sessionMetaCache struct {
	mu            sync.Mutex
	configOptions json.RawMessage
	commands      json.RawMessage
	sessionInfo   json.RawMessage
	mode          json.RawMessage
	usage         json.RawMessage
}

func (c *sessionMetaCache) store(kind acpupdate.Kind, data json.RawMessage) {
	if len(data) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch kind {
	case acpupdate.KindConfigOptions:
		c.configOptions = data
	case acpupdate.KindCommands:
		c.commands = data
	case acpupdate.KindSessionInfo:
		c.sessionInfo = data
	case acpupdate.KindMode:
		c.mode = data
	case acpupdate.KindUsage:
		c.usage = data
	}
}

func (c *sessionMetaCache) snapshot() SessionMetadata {
	c.mu.Lock()
	defer c.mu.Unlock()
	return SessionMetadata{
		ConfigOptions:     c.configOptions,
		AvailableCommands: c.commands,
		SessionInfo:       c.sessionInfo,
		Mode:              c.mode,
		Usage:             c.usage,
	}
}

// turnStartEvents builds the cached-state events replayed at the start of each
// turn, in a stable order. Live updates during the turn supersede them; a
// rare duplicate (an update racing the turn start) is harmless because every
// entry is a full snapshot of its kind.
func (c *sessionMetaCache) turnStartEvents() []TurnEvent {
	snap := c.snapshot()
	var events []TurnEvent
	for _, entry := range []struct {
		event string
		data  json.RawMessage
	}{
		{string(acpupdate.KindConfigOptions), snap.ConfigOptions},
		{string(acpupdate.KindCommands), snap.AvailableCommands},
		{string(acpupdate.KindSessionInfo), snap.SessionInfo},
		{string(acpupdate.KindMode), snap.Mode},
		{string(acpupdate.KindUsage), snap.Usage},
	} {
		if len(entry.data) > 0 {
			events = append(events, TurnEvent{Event: entry.event, Data: entry.data})
		}
	}
	return events
}

// consumeMetadata feeds the session metadata cache from a dedicated updates
// subscription. It exits when the subscription channel is closed (instance
// close unsubscribes).
func (i *instance) consumeMetadata(updates <-chan acptransport.Message) {
	for msg := range updates {
		if msg.Method != "session/update" {
			continue
		}
		for _, ev := range acpupdate.Parse(msg.Params) {
			i.meta.store(ev.Kind, ev.Data)
		}
	}
}

// cacheSessionConfigOptions stores the option snapshot in the same shape as
// a config_option_update so turn-start replay events match live ones.
func (i *instance) cacheSessionConfigOptions(result json.RawMessage) {
	var payload struct {
		ConfigOptions json.RawMessage `json:"configOptions"`
	}
	if err := json.Unmarshal(result, &payload); err != nil || len(payload.ConfigOptions) == 0 {
		return
	}
	synthesized, err := json.Marshal(map[string]json.RawMessage{
		"sessionUpdate": json.RawMessage(`"config_option_update"`),
		"configOptions": payload.ConfigOptions,
	})
	if err != nil {
		return
	}
	i.meta.store(acpupdate.KindConfigOptions, synthesized)
}

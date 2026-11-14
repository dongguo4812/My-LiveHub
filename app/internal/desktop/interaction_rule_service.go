package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/voice"
)

const interactionRulesConfig = "interaction_rules.json"

var supportedRuleEvents = map[string]bool{
	"danmaku": true, "gift": true, "super_chat": true, "guard": true,
	"follow": true, "welcome": true, "share": true, "like": true,
}

type InteractionRule struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	EventTypes       []string `json:"event_types"`
	Source           string   `json:"source"`
	Keyword          string   `json:"keyword,omitempty"`
	MinAmount        float64  `json:"min_amount,omitempty"`
	Action           string   `json:"action"`
	TextTemplate     string   `json:"text_template"`
	CooldownSeconds  int      `json:"cooldown_seconds"`
}

type InteractionRuleStatus struct {
	Total       int    `json:"total"`
	Enabled     int    `json:"enabled"`
	Triggered   uint64 `json:"triggered"`
	Executed    uint64 `json:"executed"`
	Skipped     uint64 `json:"skipped"`
	LastRule    string `json:"last_rule,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	LastAt      int64  `json:"last_at,omitempty"`
}

type InteractionRuleService struct {
	manager *config.Manager
	buffer *eventstream.Buffer
	voice *voice.VoiceService
	mu sync.RWMutex
	rules []InteractionRule
	lastRun map[string]time.Time
	status InteractionRuleStatus
	unsubscribe func()
}

func NewInteractionRuleService(manager *config.Manager, buffer *eventstream.Buffer, voiceService *voice.VoiceService) (*InteractionRuleService, error) {
	if manager == nil || buffer == nil { return nil, errors.New("互动规则配置和事件服务不能为空") }
	s := &InteractionRuleService{manager: manager, buffer: buffer, voice: voiceService, lastRun: make(map[string]time.Time)}
	value, err := manager.Load(interactionRulesConfig)
	if err != nil { return nil, fmt.Errorf("读取互动规则配置失败：%w", err) }
	if raw, ok := value["rules"]; ok {
		data, marshalErr := json.Marshal(raw)
		if marshalErr != nil { return nil, marshalErr }
		if err := json.Unmarshal(data, &s.rules); err != nil { return nil, fmt.Errorf("解析互动规则失败：%w", err) }
	}
	if err := validateInteractionRules(s.rules); err != nil { return nil, fmt.Errorf("互动规则配置无效：%w", err) }
	s.unsubscribe = buffer.Subscribe(s.handleEvent)
	return s, nil
}

func (s *InteractionRuleService) GetRules() []InteractionRule {
	if s == nil { return []InteractionRule{} }
	s.mu.RLock(); defer s.mu.RUnlock()
	result := make([]InteractionRule, len(s.rules))
	for i, rule := range s.rules { result[i] = rule; result[i].EventTypes = append([]string(nil), rule.EventTypes...) }
	return result
}

func (s *InteractionRuleService) SaveRules(rules []InteractionRule) error {
	if s == nil { return errors.New("互动规则服务不可用") }
	if err := validateInteractionRules(rules); err != nil { return err }
	data, err := json.Marshal(rules); if err != nil { return err }
	value := map[string]any{"rules": json.RawMessage(data)}
	encoded, err := json.Marshal(value); if err != nil { return err }
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil { return err }
	if err := s.manager.Save(interactionRulesConfig, normalized); err != nil { return fmt.Errorf("保存互动规则失败：%w", err) }
	s.mu.Lock()
	s.rules = make([]InteractionRule, len(rules))
	for i, rule := range rules { s.rules[i] = rule; s.rules[i].EventTypes = append([]string(nil), rule.EventTypes...) }
	s.mu.Unlock()
	return nil
}

func (s *InteractionRuleService) GetStatus() InteractionRuleStatus {
	if s == nil { return InteractionRuleStatus{} }
	s.mu.RLock(); defer s.mu.RUnlock()
	status := s.status
	status.Total = len(s.rules); status.Enabled = 0
	for _, rule := range s.rules { if rule.Enabled { status.Enabled++ } }
	return status
}

func (s *InteractionRuleService) handleEvent(event eventstream.Event) {
	if s == nil { return }
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.rules {
		if !rule.Enabled || !ruleMatches(rule, event) { continue }
		s.status.Triggered++
		now := time.Now()
		if previous := s.lastRun[rule.ID]; rule.CooldownSeconds > 0 && now.Sub(previous) < time.Duration(rule.CooldownSeconds)*time.Second {
			s.status.Skipped++; continue
		}
		if rule.Action != "voice" || s.voice == nil {
			s.status.Skipped++; s.status.LastError = "语音服务不可用"; continue
		}
		voiceStatus := s.voice.GetStatus()
		if !voiceStatus.Running || !voiceStatus.Enabled {
			s.status.Skipped++; s.status.LastError = "语音模块未运行或未启用"; continue
		}
		text := renderRuleText(rule.TextTemplate, event)
		if text == "" { s.status.Skipped++; continue }
		if err := s.voice.TestSpeech(text); err != nil {
			s.status.Skipped++; s.status.LastError = err.Error(); continue
		}
		s.lastRun[rule.ID] = now
		s.status.Executed++; s.status.LastRule = rule.Name; s.status.LastError = ""; s.status.LastAt = now.UnixMilli()
	}
}

func ruleMatches(rule InteractionRule, event eventstream.Event) bool {
	if rule.Source != "all" && rule.Source != event.Source { return false }
	found := false
	for _, eventType := range rule.EventTypes { if eventType == event.Type { found = true; break } }
	if !found { return false }
	keyword := strings.TrimSpace(rule.Keyword)
	if keyword != "" && !strings.Contains(strings.ToLower(event.Content+" "+event.GiftName+" "+event.UserName), strings.ToLower(keyword)) { return false }
	amount := event.Amount
	if event.Type == "gift" { amount = event.TotalPrice }
	if amount < rule.MinAmount { return false }
	return true
}

func renderRuleText(template string, event eventstream.Event) string {
	replacer := strings.NewReplacer(
		"{user}", event.UserName, "{content}", event.Content, "{gift}", event.GiftName,
		"{amount}", fmt.Sprintf("%.2f", func() float64 { if event.Type == "gift" { return event.TotalPrice }; return event.Amount }()),
		"{platform}", event.PlatformID,
	)
	return strings.TrimSpace(replacer.Replace(template))
}

func validateInteractionRules(rules []InteractionRule) error {
	if len(rules) > 100 { return errors.New("互动规则最多保存 100 条") }
	seen := make(map[string]bool, len(rules))
	for i, rule := range rules {
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Name) == "" { return fmt.Errorf("第 %d 条规则缺少 ID 或名称", i+1) }
		if seen[rule.ID] { return fmt.Errorf("规则 ID 重复：%s", rule.ID) }; seen[rule.ID] = true
		if len(rule.EventTypes) == 0 { return fmt.Errorf("规则“%s”至少选择一种事件类型", rule.Name) }
		for _, eventType := range rule.EventTypes { if !supportedRuleEvents[eventType] { return fmt.Errorf("规则“%s”包含不支持的事件类型：%s", rule.Name, eventType) } }
		if rule.Source != "all" && rule.Source != "live" && rule.Source != "simulation" { return fmt.Errorf("规则“%s”的事件来源无效", rule.Name) }
		if rule.MinAmount < 0 { return fmt.Errorf("规则“%s”的最低金额不能为负数", rule.Name) }
		if rule.Action != "voice" { return fmt.Errorf("规则“%s”的动作类型暂不支持", rule.Name) }
		if strings.TrimSpace(rule.TextTemplate) == "" || len([]rune(rule.TextTemplate)) > 200 { return fmt.Errorf("规则“%s”的播报文本不能为空且不能超过 200 个字符", rule.Name) }
		if rule.CooldownSeconds < 0 || rule.CooldownSeconds > 86400 { return fmt.Errorf("规则“%s”的冷却时间必须在 0 到 86400 秒之间", rule.Name) }
	}
	return nil
}

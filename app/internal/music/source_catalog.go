package music

// SourceCatalog describes built-in platform identities independently of
// provider execution. Search and lyric capabilities are dispatched through
// the active QuickJS source actions.
func SourceCatalog() []SourceSnapshot {
	return []SourceSnapshot{
		{ID: LXSearchTX, Name: "QQ音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchKW, Name: "酷我音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchKG, Name: "酷狗音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchWY, Name: "网易云音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchMG, Name: "咪咕音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
	}
}

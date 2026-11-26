package music

// SourceCatalog describes the built-in platform identities independently of
// the legacy native HTTP search providers. It remains available when search
// execution is fully delegated to QuickJS source actions.
func SourceCatalog() []SourceSnapshot {
	return []SourceSnapshot{
		{ID: LXSearchTX, Name: "QQ音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchKW, Name: "酷我音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchKG, Name: "酷狗音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchWY, Name: "网易云音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
		{ID: LXSearchMG, Name: "咪咕音乐", Capabilities: SourceCapabilities{Search: true}, Enabled: true, Available: true},
	}
}

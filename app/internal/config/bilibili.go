package config

import "fmt"

type BilibiliConfig struct {
	RoomID int `json:"room_id"`
}

func DefaultBilibiliConfig() BilibiliConfig { return BilibiliConfig{} }

func (c BilibiliConfig) Validate() error {
	if c.RoomID < 0 || c.RoomID > 2_147_483_647 {
		return fmt.Errorf("invalid Bilibili room ID %d", c.RoomID)
	}
	return nil
}

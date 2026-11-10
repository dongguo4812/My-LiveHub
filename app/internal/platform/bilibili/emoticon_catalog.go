package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dongguo4812/My-LiveHub/app/internal/media/resource"
)

const roomEmoticonEndpoint = "https://api.live.bilibili.com/xlive/web-ucenter/v2/emoticon/GetEmoticons"

type roomEmoticonResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Packages []struct {
			PackageID  int    `json:"pkg_id"`
			PackageName string `json:"pkg_name"`
			PackageType int    `json:"pkg_type"`
			Emoticons []struct {
				ID       int    `json:"emoticon_id"`
				Unique   string `json:"emoticon_unique"`
				Emoji    string `json:"emoji"`
				URL      string `json:"url"`
				Width    int    `json:"width"`
				Height   int    `json:"height"`
			} `json:"emoticons"`
		} `json:"data"`
	} `json:"data"`
}

func emoticonSourceIDForPackage(roomID int, unique string, packageType int) string {
	value := strings.TrimSpace(unique)
	switch {
	case strings.HasPrefix(value, "room_"):
		return strconv.Itoa(roomID)
	case strings.HasPrefix(value, "emoji_"), strings.HasPrefix(value, "official_"):
		return "common"
	}

	// GetEmoticons currently identifies general packages with pkg_type=1
	// and room-specific packages with pkg_type=2. Keep unknown types room-scoped
	// rather than accidentally sharing a room-only emoticon globally.
	switch packageType {
	case 1:
		return "common"
	case 2:
		return strconv.Itoa(roomID)
	default:
		return strconv.Itoa(roomID)
	}
}

// emoticonSourceID is used for live danmaku payloads, which may not contain
// package metadata. Unknown identities are conservatively kept room-scoped.
func emoticonSourceID(roomID int, unique string) string {
	return emoticonSourceIDForPackage(roomID, unique, 0)
}

func emoticonResourceID(unique string, packageID, emoticonID int, imageURL string) string {
	if value := strings.TrimSpace(unique); value != "" {
		return value
	}
	if packageID > 0 && emoticonID > 0 {
		return fmt.Sprintf("pkg_%d_emoticon_%d", packageID, emoticonID)
	}
	// Without the official unique key or both numeric identifiers, URL is the
	// only stable fallback available from the catalog response.
	return imageURL
}

func fetchRoomEmoticonResources(ctx context.Context, httpClient *http.Client, roomID int, cookie string) ([]resource.Resource, error) {
	if roomID <= 0 {
		return nil, fmt.Errorf("invalid Bilibili room ID for emoticon catalog")
	}
	if strings.TrimSpace(cookie) == "" {
		return nil, fmt.Errorf("Bilibili login cookie is required for emoticon catalog")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	endpoint, err := url.Parse(roomEmoticonEndpoint)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("platform", "pc")
	query.Set("room_id", strconv.Itoa(roomID))
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Cookie", cookie)
	request.Header.Set("User-Agent", "Mozilla/5.0 My-LiveHub")
	request.Header.Set("Referer", "https://live.bilibili.com/")

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Bilibili emoticon catalog returned HTTP %d", response.StatusCode)
	}

	var payload roomEmoticonResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("Bilibili emoticon catalog API error %d: %s", payload.Code, payload.Message)
	}

	items := make([]resource.Resource, 0)
	sortOrder := 0
	for _, pkg := range payload.Data.Packages {
		for _, emoticon := range pkg.Emoticons {
			sortOrder++
			imageURL := normalizeEmoticonURL(emoticon.URL)
			if imageURL == "" {
				continue
			}
			id := emoticonResourceID(emoticon.Unique, pkg.PackageID, emoticon.ID, imageURL)
			name := strings.TrimSpace(emoticon.Emoji)
			if name == "" {
				name = id
			}
			items = append(items, resource.Resource{
				PlatformID: "bilibili",
				SourceID:   emoticonSourceIDForPackage(roomID, emoticon.Unique, pkg.PackageType),
				Type:       resource.TypeEmoticon,
				ID:         id,
				Name:       name,
				ImageURL:   imageURL,
				Width:      emoticon.Width,
				Height:     emoticon.Height,
				SortOrder:  sortOrder,
			})
		}
	}
	return items, nil
}

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type PlaylistItemsResponse struct {
	Items         []PlaylistItem `json:"items"`
	NextPageToken string         `json:"nextPageToken"`
}

type PlaylistItem struct {
	Snippet Snippet `json:"snippet"`
}

type Snippet struct {
	Title       string     `json:"title"`
	PublishedAt string     `json:"publishedAt"`
	ResourceId  ResourceId `json:"resourceId"`
}

type ResourceId struct {
	VideoId string `json:"videoId"`
}

type YoutubePlaylist struct {
	Playlist  []Video   `json:"videos"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func newPlaylist(items []Video) *YoutubePlaylist {
	return &YoutubePlaylist{Playlist: items, UpdatedAt: time.Now()}
}

func (p YoutubePlaylist) subtract(playlist YoutubePlaylist) *YoutubePlaylist {
	playlistMap := make(map[string]Video)
	for _, video := range p.Playlist {
		playlistMap[video.VideoId] = video
	}

	var diff []Video
	for _, video := range playlist.Playlist {
		v, found := playlistMap[video.VideoId]
		if !found {
			diff = append(diff, video)
		}
		if found && v.Title != video.Title && (v.Title == "Deleted video" || video.Title == "Deleted video") {
			diff = append(diff, video)
		}
	}

	return newPlaylist(diff)
}

type Video struct {
	Title       string    `json:"title"`
	VideoId     string    `json:"videoId"`
	PublishedAt time.Time `json:"publishedAt"`
}

func newVideo(item *PlaylistItem) *Video {
	parsedTime, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
	if err != nil {
		log.Fatalf("Error parsing time: %v", err)
	}

	return &Video{Title: item.Snippet.Title, VideoId: item.Snippet.ResourceId.VideoId, PublishedAt: parsedTime}

}

func fetchPlaylistItems(apiKey, playlistID, pageToken string) (*PlaylistItemsResponse, error) {
	baseURL := "https://www.googleapis.com/youtube/v3/playlistItems"
	params := url.Values{}
	params.Set("part", "snippet")
	params.Set("maxResults", "50")
	params.Set("playlistId", playlistID)
	params.Set("key", apiKey)
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}
	url := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API call failed, status code: %d", resp.StatusCode)
	}

	var response PlaylistItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response, nil
}

func writeFile(playlist *YoutubePlaylist, dirPath string, fileName string) {

	jsonData, err := json.MarshalIndent(playlist, "", "  ")
	if err != nil {
		fmt.Println("Error marshaling JSON:", err)
		return
	}

	filePath := filepath.Join(dirPath, fileName)

	file, err := os.Create(filePath)
	if err != nil {
		log.Fatalf("Error creating file: %v", err)
	}
	defer file.Close()

	_, err = file.Write(jsonData)
	if err != nil {
		log.Fatalf("Error writing JSON to file: %v", err)
	}

	fmt.Println("JSON data written to", filePath)
}

func readPlaylistFromFile(config Config, fileName string) (YoutubePlaylist, error) {
	var youtubePlaylist YoutubePlaylist

	filePath := filepath.Join(config.DirPath, fileName)

	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return youtubePlaylist, err
	}

	err = json.Unmarshal(fileData, &youtubePlaylist)
	if err != nil {
		return youtubePlaylist, fmt.Errorf("error unmarshalling JSON: %w", err)
	}

	return youtubePlaylist, nil
}

type Config struct {
	ApiKey           string `json:"apiKey"`
	PlaylistId       string `json:"playlistId"`
	DirPath          string `json:"dirPath"`
	DiffFileName     string `json:"diffFileName"`
	PlaylistFileName string `json:"playlistFileName"`
	KeepHistory      bool   `json:"keepHistory"`
}

func newConfig() *Config {
	file, err := os.Open("config.json")
	if err != nil {
		log.Fatalf("Failed to open config file: %v", err)
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}

	var config Config
	if err := json.Unmarshal(bytes, &config); err != nil {
		log.Fatalf("Failed to unmarshal config file: %v", err)
	}
	if config.DirPath == "" {
		wd, _ := os.Getwd()
		config.DirPath = wd
	}

	if config.DiffFileName == "" {
		config.DiffFileName = "diff.json"
	}
	if config.PlaylistFileName == "" {
		config.PlaylistFileName = "playlist.json"
	}

	fmt.Printf("Config: %+v\n", config)
	return &config
}

func (config Config) saveHistory(oldDiff YoutubePlaylist, oldPlaylist YoutubePlaylist) {
	fileName := fmt.Sprintf("%s_%s", oldPlaylist.UpdatedAt.Format(time.RFC3339), config.PlaylistFileName)
	writeFile(&oldPlaylist, config.DirPath, fileName)
	if oldDiff.Playlist != nil {
		diffFileName := fmt.Sprintf("%s_%s", oldDiff.UpdatedAt.Format(time.RFC3339), config.DiffFileName)
		writeFile(&oldDiff, config.DirPath, diffFileName)
		os.Remove(filepath.Join(config.DirPath, config.DiffFileName))

	}
}

func main() {
	config := newConfig()
	var videos []Video

	pageToken := ""

	for {
		response, err := fetchPlaylistItems(config.ApiKey, config.PlaylistId, pageToken)
		if err != nil {
			log.Fatalf("Error fetching playlist items: %v", err)
		}

		for _, item := range response.Items {
			video := *newVideo(&item)
			videos = append(videos, video)
		}

		if response.NextPageToken == "" {
			break
		}
		pageToken = response.NextPageToken
	}
	playlist := newPlaylist(videos)
	oldPlaylist, err := readPlaylistFromFile(*config, config.PlaylistFileName)
	oldDiff, _ := readPlaylistFromFile(*config, config.DiffFileName)

	if err != nil {
		log.Printf("Error fetching playlist %s a new playlist will be created", err)
		writeFile(playlist, config.DirPath, config.PlaylistFileName)
		return
	}

	diff := playlist.subtract(oldPlaylist)

	if diff.Playlist == nil {
		if len(playlist.Playlist) != len(oldPlaylist.Playlist) {
			if config.KeepHistory {
				config.saveHistory(oldDiff, oldPlaylist)
			}
			writeFile(playlist, config.DirPath, config.PlaylistFileName)
			log.Println("Only new videos were found")
			return
		} else {
			log.Println("No diff and no new videos, nothing to do")
			return
		}
	}

	if config.KeepHistory {
		config.saveHistory(oldDiff, oldPlaylist)
	}

	writeFile(playlist, config.DirPath, config.PlaylistFileName)
	writeFile(diff, config.DirPath, config.DiffFileName)
}

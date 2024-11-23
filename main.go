package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

type Streamers struct {
	Streamers []Streamer `json:"streamers"`
}

type Streamer struct {
	Username string `json:"username"`
	Running  bool   `json:"running"`
}

type IndexPageData struct {
	Streamers []Streamer `json:"streamers"`
	PageTitle string
	Started   bool
	ClientUrl string
}

type ActionPageData struct {
	Username  string
	PageTitle string
}

type StreamerResponse struct {
	Model struct {
		Status string `json:"status"`
	} `json:"model"`
	IsCamAvailable bool `json:"isCamAvailable"`
	Cam            struct {
		IsCamActive bool   `json:"isCamActive"`
		StreamName  string `json:"streamName"`
	} `json:"cam"`
}

var wg sync.WaitGroup
var streamers Streamers

// default config variables
var dateTimeFormat = ""
var minFileAgeForMerging = time.Hour * 3
var configFilePath = "./streamers.json"
var port = ""
var streamSite = ""
var cdnUrl = ""
var started = true
var clientUrl = ""
var downloadDir = "./downloads"

func init() {
	err := godotenv.Load()
	if err != nil {
		fmt.Printf("[ERROR] unable to open env file: %s\n", err)
	}

	dateTimeFormat = os.Getenv("DATE_TIME_FORMAT")
	port = (os.Getenv("PORT"))
	streamSite = os.Getenv("STREAM_SITE")
	cdnUrl = os.Getenv("CDN_URL")
	clientUrl = os.Getenv("CLIENT_URL")
	downloadDir = os.Getenv("DOWNLOAD_DIR")

	jsonFile, err := os.Open(configFilePath)
	if err != nil {
		fmt.Printf("[ERROR] unable to open config file: %s\n", err)
		return
	}
	defer jsonFile.Close()

	byteValue, err := io.ReadAll(jsonFile)
	if err != nil {
		fmt.Printf("[ERROR] unable to read config file: %s\n", err)
		return
	}

	err = json.Unmarshal(byteValue, &streamers)
	if err != nil {
		fmt.Printf("[ERROR] unable to unmarshall config file: %s\n", err)
		return
	}
}

func main() {

	handleHttpRequests()

	fmt.Println("[MONITOR] starting up...")

	if !hasAvaliableDiskSpace() {
		return
	}

	wg.Add(1)
	go exitIfNoDiskSpace()
	go startMerger()
	for i := 0; i < len(streamers.Streamers); i++ {
		wg.Add(1)
		go handleRecording(streamers.Streamers[i].Username)
	}

	wg.Wait()
}

func startMerger() {
	fmt.Println("[MONITOR] starting merger...")
	for {
		if started {
			currentTime := time.Now().UTC()
			if currentTime.Minute() == 0 || currentTime.Minute() == 30 {
				fmt.Println("[MONITOR] starting merge...")
				files, err := os.ReadDir(downloadDir)
				if err != nil {
					fmt.Printf("[ERROR] unable to read download dir: %s\n", err)
					return
				}

				for _, file := range files {
					if file.IsDir() {
						streamerUsername := file.Name()
						streamerFiles, err := os.ReadDir(fmt.Sprintf("%s/%s", downloadDir, file.Name()))
						if err != nil {
							fmt.Printf("[ERROR] unable to read streamer dir (%s): %s\n", streamerUsername, err)
							return
						}

						for _, videoFile := range streamerFiles {
							if !videoFile.IsDir() {
								videoFileInfo, err := os.Stat(fmt.Sprintf("%s/%s/%s", downloadDir, streamerUsername, videoFile.Name()))
								if err != nil {
									fmt.Printf("[ERROR] unable to stat streamer video file (%s): %s\n", streamerUsername, err)
									return
								}
								lastModifiedTime := videoFileInfo.ModTime()
								add := lastModifiedTime.Add(minFileAgeForMerging).Compare(currentTime)
								if strings.Contains(videoFile.Name(), "compressed") && add == -1 {
									ffmpegMergeList, err := os.OpenFile(fmt.Sprintf("%s/%s/toMerge.txt", downloadDir, streamerUsername), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
									if err != nil {
										fmt.Printf("[ERROR] unable to open ffmpeg merge list (%s): %s\n", streamerUsername, err)
										return
									}
									ffmpegMergeList.WriteString(fmt.Sprintf("file './%s'\n", videoFile.Name()))
									ffmpegMergeList.Close()
								}
							}
						}
						wg.Add(1)
						go mergeAndCleanup(streamerUsername, currentTime)
					}
				}
				time.Sleep(time.Minute)
			} else {
				time.Sleep(time.Second * 59)
			}
		}
	}
}

func mergeAndCleanup(streamerUsername string, currentTime time.Time) {
	mergeListFilePath := fmt.Sprintf("%s/%s/toMerge.txt", downloadDir, streamerUsername)
	outputFileName := fmt.Sprintf("%s/%s/MERGED_%s.mkv", downloadDir, streamerUsername, currentTime.Format(dateTimeFormat))
	ffmpegMerge(mergeListFilePath, outputFileName)
	generateThumbnail(outputFileName)
	err := os.Remove(mergeListFilePath)
	if err != nil {
		fmt.Printf("[ERROR] unable to delete ffmpeg merge list (%s): %s\n", streamerUsername, err)
		return
	}
	deleteFiles, err := os.ReadDir(fmt.Sprintf("%s/%s", downloadDir, streamerUsername))
	if err != nil {
		fmt.Printf("[ERROR] unable to read streamer directory (%s): %s\n", streamerUsername, err)
		return
	}
	for _, deleteFile := range deleteFiles {
		if !deleteFile.IsDir() && strings.Contains(deleteFile.Name(), "compressed") && fileIsOldEnough(fmt.Sprintf("%s/%s/%s", downloadDir, streamerUsername, deleteFile.Name()), currentTime) {
			fmt.Printf("[Monitor] deleting video file: %s\n", deleteFile.Name())
			err = os.Remove(fmt.Sprintf("%s/%s/%s", downloadDir, streamerUsername, deleteFile.Name()))
			if err != nil {
				fmt.Printf("[ERROR] unable to delete video file (%s): %s\n", streamerUsername, err)
				return
			}
		}
	}
}

func handleRecording(username string) {
	for {
		if started {
			streamerUrl := fmt.Sprintf("%s/%s", streamSite, url.QueryEscape(username))
			req, err := http.NewRequest("GET", streamerUrl, nil)
			if err != nil {
				fmt.Printf("[ERROR] unable to create request (%s): %s\n", username, err)
				return
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:75.0) Gecko/20100101 Firefox/75.0")
			client := &http.Client{}
			res, err := client.Do(req)
			if err != nil {
				fmt.Printf("[ERROR] unable to make http request (%s): %s\n", username, req.URL)
				fmt.Printf("[ERROR] unable to make http request (%s): %s\n", username, err)
				handleRecording(username)
			}
			switch res.StatusCode {
			case 200:
				body, err := io.ReadAll(res.Body)
				if err != nil {
					fmt.Printf("[ERROR] unable to read body (%s): %s\n", username, err)
				}
				defer res.Body.Close()

				var streamerData StreamerResponse
				err = json.Unmarshal(body, &streamerData)
				if err != nil {
					fmt.Printf("[ERROR] unable to unmarshall JSON response (%s): %s\n", username, err)
					return
				}
				stream_url := fmt.Sprintf("%s/%s/master/%s.m3u8", cdnUrl, streamerData.Cam.StreamName, streamerData.Cam.StreamName)

				if streamerData.IsCamAvailable {
					for i := 0; i < len(streamers.Streamers); i++ {
						if streamers.Streamers[i].Username == username {
							streamers.Streamers[i].Running = true
						}
					}
					currentTime := time.Now().UTC().Format(dateTimeFormat)
					err = os.MkdirAll(fmt.Sprintf("%s/%s", downloadDir, username), os.ModePerm)
					if err != nil {
						fmt.Printf("[ERROR] unable to create directory (%s): %s\n", username, err)
					}
					streamVideoFile(stream_url, fmt.Sprintf("%s/%s/%s_%s.mkv", downloadDir, username, username, currentTime))
					go handleCompression(username, currentTime)
				} else {
					for i := 0; i < len(streamers.Streamers); i++ {
						if streamers.Streamers[i].Username == username {
							streamers.Streamers[i].Running = false
						}
					}
					fmt.Printf("[SLEEPING] %s\n", username)
					time.Sleep(time.Second * 60)
				}

			case 404:
				fmt.Printf("[NOT FOUND] %s\n", username)
				time.Sleep(time.Second * 60)
			default:
				fmt.Printf("[UNKNOWN] %s\n", username)
				time.Sleep(time.Second * 60)
			}
		}
	}

}

func handleCompression(username string, UTCtime string) {
	compressVideoFile(fmt.Sprintf("%s/%s/%s_%s.mkv", downloadDir, username, username, UTCtime), fmt.Sprintf("%s/%s/%s_compressed_%s.mkv", downloadDir, username, username, UTCtime))
	err := os.Remove(fmt.Sprintf("%s/%s/%s_%s.mkv", downloadDir, username, username, UTCtime))
	if err != nil {
		fmt.Printf("[ERROR] unable to delete video after compression: %s\n", err)
	}
}

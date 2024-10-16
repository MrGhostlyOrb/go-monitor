package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/joho/godotenv"
	"golang.org/x/sys/unix"
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

func init() {
	err := godotenv.Load()
	if err != nil {
		fmt.Printf("[ERROR] unable to open env file: %s\n", err)
	}

	dateTimeFormat = os.Getenv("DATE_TIME_FORMAT")
	port = (os.Getenv("PORT"))
	streamSite = os.Getenv("STREAM_SITE")
	cdnUrl = os.Getenv("CDN_URL")

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

func handleHttpRequests() {

	http.HandleFunc("/delete-merged", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("[MONITOR] starting delete...")
		files, err := os.ReadDir(".")
		if err != nil {
			fmt.Printf("[ERROR] unable to read top level dir: %s\n", err)
			return
		}

		for _, file := range files {
			if file.IsDir() {
				streamerUsername := file.Name()
				streamerFiles, err := os.ReadDir(file.Name())
				if err != nil {
					fmt.Printf("[ERROR] unable to read streamer dir (%s): %s\n", streamerUsername, err)
					return
				}

				for _, videoFile := range streamerFiles {
					if !videoFile.IsDir() && strings.Contains(videoFile.Name(), "merged") {
						err := os.Remove(fmt.Sprintf("./%s/%s", streamerUsername, videoFile.Name()))
						if err != nil {
							fmt.Printf("[ERROR] unable to remove merged streamer video file (%s): %s\n", streamerUsername, err)
							return
						}
						fmt.Printf("[MONITOR] deleted merged streamer video file (%s): %s\n", streamerUsername, videoFile.Name())
					}
				}
				streamerFiles, err = os.ReadDir(file.Name())
				if err != nil {
					fmt.Printf("[ERROR] unable to read streamer dir (%s): %s\n", streamerUsername, err)
					return
				}
				if len(streamerFiles) == 0 {
					err = os.Remove(file.Name())
					if err != nil {
						fmt.Printf("[ERROR] unable to remove streamer directory (%s): %s\n", streamerUsername, err)
						return
					}
					fmt.Printf("[MONITOR] deleted empty streamer directory: %s\n", streamerUsername)
				}
			}
		}
		pageData := ActionPageData{
			PageTitle: "Deleted Merged",
			Username:  "N/A",
		}

		tmpl, err := template.ParseFiles("action.html")
		if err != nil {
			http.Error(w, "[ERROR] unable to parse template:", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, pageData)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		pageData := IndexPageData{
			PageTitle: "Streamers",
			Streamers: streamers.Streamers,
			Started:   started,
		}

		tmpl, err := template.ParseFiles("index.html")
		if err != nil {
			fmt.Printf("[ERROR] unable to parse template: %s\n", err)
			http.Error(w, "[ERROR] unable to parse template:", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, pageData)
	})

	http.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		queryParams := r.URL.Query()
		username := queryParams.Get("username")

		if username == "" {
			http.Error(w, "[ERROR] username is required", http.StatusBadRequest)
			return
		}

		file, err := os.OpenFile(configFilePath, os.O_RDWR, 0644)
		if err != nil {
			http.Error(w, "[ERROR] unable to open config file", http.StatusInternalServerError)

			return
		}
		defer file.Close()

		found := false
		for i, stremerEntries := range streamers.Streamers {
			if stremerEntries.Username == username {
				streamers.Streamers = append(streamers.Streamers[:i], streamers.Streamers[i+1:]...)
				found = true
				break
			}
		}

		if !found {
			http.Error(w, "[ERROR] user not found in config file", http.StatusNotFound)
			return
		}

		file.Truncate(0)
		file.Seek(0, 0)
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "    ")
		err = encoder.Encode(Streamers{streamers.Streamers})
		if err != nil {
			http.Error(w, "[ERROR] failed to encode config json", http.StatusInternalServerError)
			return
		}

		pageData := ActionPageData{
			PageTitle: "Deleted",
			Username:  username,
		}

		tmpl, err := template.ParseFiles("action.html")
		if err != nil {
			http.Error(w, "[ERROR] unable to parse template:", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, pageData)
	})

	http.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		queryParams := r.URL.Query()
		username := queryParams.Get("username")

		if username == "" {
			http.Error(w, "[ERROR] username is required", http.StatusBadRequest)
			return
		}

		file, err := os.OpenFile(configFilePath, os.O_RDWR, 0644)
		if err != nil {
			http.Error(w, "[ERROR] unable to open config file", http.StatusInternalServerError)

			return
		}
		defer file.Close()

		streamer := Streamer{Username: username, Running: false}
		streamers.Streamers = append(streamers.Streamers[:len(streamers.Streamers)], streamer)

		err = file.Truncate(0)
		if err != nil {
			http.Error(w, "[ERROR] failed to truncate config json", http.StatusInternalServerError)
			return
		}
		_, err = file.Seek(0, 0)
		if err != nil {
			http.Error(w, "[ERROR] failed to seek config json", http.StatusInternalServerError)
			return
		}
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "    ")
		err = encoder.Encode(Streamers{streamers.Streamers})
		if err != nil {
			http.Error(w, "[ERROR] failed to encode config json", http.StatusInternalServerError)
			return
		}

		pageData := ActionPageData{
			PageTitle: "Added",
			Username:  username,
		}

		tmpl, err := template.ParseFiles("action.html")
		if err != nil {
			http.Error(w, "[ERROR] unable to parse template:", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, pageData)
	})

	http.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		started = true
		fmt.Println("[MONITOR] starting...")

		pageData := ActionPageData{
			PageTitle: "Started",
			Username:  "N/A",
		}

		tmpl, err := template.ParseFiles("action.html")
		if err != nil {
			http.Error(w, "[ERROR] unable to parse template:", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, pageData)
	})

	http.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		started = false
		fmt.Println("[MONITOR] stopping...")

		pageData := ActionPageData{
			PageTitle: "Stopped",
			Username:  "N/A",
		}

		tmpl, err := template.ParseFiles("action.html")
		if err != nil {
			http.Error(w, "[ERROR] unable to parse template:", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, pageData)
	})
	wg.Add(1)
	go http.ListenAndServe(fmt.Sprintf(":%s", port), nil)
}

func startMerger() {
	fmt.Println("[MONITOR] starting merger...")
	for {
		if started {
			currentTime := time.Now().UTC()
			if currentTime.Minute() == 0 || currentTime.Minute() == 30 {
				fmt.Println("[MONITOR] starting merge...")
				files, err := os.ReadDir(".")
				if err != nil {
					fmt.Printf("[ERROR] unable to read top level dir: %s\n", err)
					return
				}

				for _, file := range files {
					if file.IsDir() {
						streamerUsername := file.Name()
						streamerFiles, err := os.ReadDir(file.Name())
						if err != nil {
							fmt.Printf("[ERROR] unable to read streamer dir (%s): %s\n", streamerUsername, err)
							return
						}

						for _, videoFile := range streamerFiles {
							if !videoFile.IsDir() {
								videoFileInfo, err := os.Stat(fmt.Sprintf("./%s/%s", streamerUsername, videoFile.Name()))
								if err != nil {
									fmt.Printf("[ERROR] unable to stat streamer video file (%s): %s\n", streamerUsername, err)
									return
								}
								lastModifiedTime := videoFileInfo.ModTime()
								add := lastModifiedTime.Add(minFileAgeForMerging).Compare(currentTime)
								if strings.Contains(videoFile.Name(), "compressed") && add == -1 {
									ffmpegMergeList, err := os.OpenFile(fmt.Sprintf("./%s/toMerge.txt", streamerUsername), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
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

func ffmpegMerge(mergeListFilePath string, outputFileName string) {
	command := exec.Command("ffmpeg", "-fflags", "+genpts", "-f", "concat", "-safe", "0", "-i", mergeListFilePath, "-c", "copy", outputFileName)
	fmt.Printf("[FFMPEG] merging... %s\n", command.String())
	err := command.Run()
	if err != nil {
		fmt.Printf("[ERROR] error in merge command: %s\n", err)
		return
	}
}

func fileIsOldEnough(filePath string, currentTime time.Time) bool {
	videoFileInfo, err := os.Stat(filePath)
	if err != nil {
		fmt.Printf("[ERROR] unable to stat file (%s): %s\n", filePath, err)
		return false
	}
	lastModifiedTime := videoFileInfo.ModTime()
	add := lastModifiedTime.Add(minFileAgeForMerging).Compare(currentTime)
	if add == -1 {
		return true
	} else {
		return false
	}
}

func mergeAndCleanup(streamerUsername string, currentTime time.Time) {
	mergeListFilePath := fmt.Sprintf("./%s/toMerge.txt", streamerUsername)
	outputFileName := fmt.Sprintf("./%s/merged_%s.mkv", streamerUsername, currentTime.Format(dateTimeFormat))
	ffmpegMerge(mergeListFilePath, outputFileName)
	err := os.Remove(mergeListFilePath)
	if err != nil {
		fmt.Printf("[ERROR] unable to delete ffmpeg merge list (%s): %s\n", streamerUsername, err)
		return
	}
	deleteFiles, err := os.ReadDir(streamerUsername)
	if err != nil {
		fmt.Printf("[ERROR] unable to read streamer directory (%s): %s\n", streamerUsername, err)
		return
	}
	for _, deleteFile := range deleteFiles {
		if !deleteFile.IsDir() && strings.Contains(deleteFile.Name(), "compressed") && fileIsOldEnough(fmt.Sprintf("./%s/%s", streamerUsername, deleteFile.Name()), currentTime) {
			fmt.Printf("[Monitor] deleting video file: %s\n", deleteFile.Name())
			err = os.Remove(fmt.Sprintf("%s/%s", streamerUsername, deleteFile.Name()))
			if err != nil {
				fmt.Printf("[ERROR] unable to delete video file (%s): %s\n", streamerUsername, err)
				return
			}
		}
	}
}

func hasAvaliableDiskSpace() bool {
	var stat unix.Statfs_t

	wd, err := os.Getwd()
	if err != nil {
		fmt.Println("[ERROR] unable to get working dir...")
		return false
	}

	err = unix.Statfs(wd, &stat)
	if err != nil {
		fmt.Println("[ERROR] unable to stat directory...")
		return false
	}

	if stat.Bavail*uint64(stat.Bsize) < 10000000000 {
		fmt.Println("disk space too low exiting...")
		return false
	} else {
		return true
	}
}

func exitIfNoDiskSpace() {
	for {
		if !hasAvaliableDiskSpace() {
			fmt.Println("exiting...")
			os.Exit(0)
		} else {
			time.Sleep(time.Second)
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
				sc_url_stream := fmt.Sprintf("%s/%s/master/%s.m3u8", cdnUrl, streamerData.Cam.StreamName, streamerData.Cam.StreamName)

				if streamerData.IsCamAvailable {
					for i := 0; i < len(streamers.Streamers); i++ {
						if streamers.Streamers[i].Username == username {
							streamers.Streamers[i].Running = true
						}
					}
					currentTime := time.Now().UTC().Format(dateTimeFormat)
					err = os.MkdirAll(username, os.ModePerm)
					if err != nil {
						fmt.Printf("[ERROR] unable to create directory (%s): %s\n", username, err)
					}
					streamVideoFile(sc_url_stream, fmt.Sprintf("./%s/%s_%s.mkv", username, username, currentTime))
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
			}
		}
	}

}

func streamVideoFile(url string, outputFileName string) {
	command := exec.Command("ffmpeg", "-i", url, "-c:v", "copy", "-c:a", "copy", outputFileName)
	fmt.Printf("[FFMPEG] streaming... %s\n", command.String())
	err := command.Run()
	if err != nil {
		fmt.Printf("[ERROR] unable to stream video... retrying in 10s (%s): %s\n", url, err)
		time.Sleep(time.Second * 10)
	}
}

func compressVideoFile(filepath string, outputFileName string) {
	command := exec.Command("ffmpeg", "-i", filepath, "-c:v", "libx264", "-crf", "35", "-c:a", "aac", "-b:a", "128k", outputFileName)
	fmt.Printf("[FFMPEG] compressing... %s\n", command.String())
	err := command.Run()
	if err != nil {
		fmt.Printf("[ERROR] unable to compress video (%s): %s\n", filepath, err)
	}
}

func handleCompression(username string, UTCtime string) {

	compressVideoFile(fmt.Sprintf("./%s/%s_%s.mkv", username, username, UTCtime), fmt.Sprintf("./%s/%s_compressed_%s.mkv", username, username, UTCtime))
	err := os.Remove(fmt.Sprintf("./%s/%s_%s.mkv", username, username, UTCtime))
	if err != nil {
		fmt.Printf("[ERROR] unable to delete video after compression: %s\n", err)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"
)

func handleHttpRequests() {

	http.HandleFunc("/delete-merged", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("[MONITOR] starting delete...")
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
					fileInfo, _ := os.Stat(fmt.Sprintf("%s/%s/%s", downloadDir, streamerUsername, videoFile.Name()))
					if !videoFile.IsDir() && strings.Contains(videoFile.Name(), "MERGED") || (!videoFile.IsDir() && !strings.Contains(videoFile.Name(), "MERGED") && !strings.Contains(videoFile.Name(), "compressed") && time.Since(fileInfo.ModTime()) > 24*time.Hour) {
						err := os.Remove(fmt.Sprintf("%s/%s/%s", downloadDir, streamerUsername, videoFile.Name()))
						if err != nil {
							fmt.Printf("[ERROR] unable to remove merged streamer video file (%s): %s\n", streamerUsername, err)
							return
						}
						fmt.Printf("[MONITOR] deleted merged streamer video file (%s): %s\n", streamerUsername, videoFile.Name())
					}
				}
				streamerFiles, err = os.ReadDir(fmt.Sprintf("%s/%s", downloadDir, file.Name()))
				if err != nil {
					fmt.Printf("[ERROR] unable to read streamer dir (%s): %s\n", streamerUsername, err)
					return
				}
				if len(streamerFiles) == 0 {
					err = os.Remove(fmt.Sprintf("%s/%s", downloadDir, file.Name()))
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
			ClientUrl: clientUrl,
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
		wg.Add(1)
		go handleRecording(username)

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

	http.HandleFunc("/remove", func(w http.ResponseWriter, r *http.Request) {
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

		for i, streamer := range streamers.Streamers {
			if streamer.Username == username {
				streamers.Streamers = append(streamers.Streamers[:i], streamers.Streamers[i+1:]...)
				break
			}
		}

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
			PageTitle: "Removed",
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

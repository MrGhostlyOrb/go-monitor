package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func compressVideoFile(filepath string, outputFileName string) {
	command := exec.Command("ffmpeg", "-i", filepath, "-c:v", "libx264", "-crf", "35", "-c:a", "aac", "-b:a", "128k", outputFileName)
	fmt.Printf("[FFMPEG] compressing... %s\n", command.String())
	err := command.Run()
	if err != nil {
		fmt.Printf("[ERROR] unable to compress video (%s): %s\n", filepath, err)
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

func generateThumbnail(outputFileName string) {
	imageFileName := fmt.Sprintf("%s_MERGED.png", outputFileName)

	command := exec.Command("ffmpeg", "-i", outputFileName, "-vframes", "1", imageFileName)
	err := command.Run()
	if err != nil {
		fmt.Println("Error generating thumbnail: ", err)
		return
	}

	fmt.Printf("Thumbnail saved successfully as: %s\n", imageFileName)
}

func ffmpegMerge(mergeListFilePath string, outputFileName string) {
	command := exec.Command("ffmpeg", "-fflags", "+genpts", "-f", "concat", "-safe", "0", "-i", mergeListFilePath, "-c", "copy", outputFileName)
	fmt.Printf("[FFMPEG] merging... %s\n", command.String())
	err := command.Run()
	if err != nil {
		fmt.Printf("[ERROR] error running merge command: %s\n", err)
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

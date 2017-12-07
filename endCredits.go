package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type WalkFunc func(path string, info os.FileInfo, err error) error
type done struct{}
type AnalysisType struct {
	EvalTime float64 `json:"evaluationTime"`
	Credits  float64 `json:"credits"`
	Scene    float64 `json:"scene"`
}

var _cacheDir string
var _results []int

const _SAMPLE_SIZE = 10
const _MEAN_THRESHOLD = 0.7 // this means that in 10 seconds at least 1/2 of it should be credit scenes
const _CREDITS_DURATION_THRESHOLD = 600
const _THREADS_THRESHOLD = 10

func main() {
	srcPtr := flag.String("src", "", "location of src to be analysed")
	flag.Parse()

	src := *srcPtr
	duration := getDurationInSeconds(src)
	_cacheDir = makeCacheDir(src, true)

	seekTo := 0
	if duration > _CREDITS_DURATION_THRESHOLD {
		seekTo = duration - _CREDITS_DURATION_THRESHOLD
	}

	_results = make([]int, duration-seekTo)

	mp4ToStills(src, _cacheDir, seekTo)
	walkDirectory(_cacheDir, analyseCredits)

	index := getIndex()
	log.Printf("index: %d", index)
	log.Printf("Closing Credits start at %d seconds", seekTo+index)

	//	removeCacheDir(_cacheDir)
}

func getDurationInSeconds(src string) (seconds int) {

	//cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", src)
	cmd := exec.Command("bash", "-c", "ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 "+src)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Println("Getting duration...")

	err := cmd.Run()
	if err != nil {
		log.Fatalf("%s, %s", stderr.String(), err.Error())
	}

	// we are dropping the milliseconds
	seconds, _ = strconv.Atoi(strings.Split(stdout.String(), ".")[0])

	log.Printf("Getting duration... Done [%d]", seconds)

	return
}

func mp4ToStills(src string, cacheDir string, t int) {

	cmd := exec.Command("ffmpeg", "-ss", fmt.Sprintf("%d", t), "-i", src, "-vf", "fps=1", cacheDir+"/%3d.jpeg")
	var out, errB bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errB

	err := cmd.Start()
	if err != nil {
		log.Fatalf("%s, %s", errB.String(), err.Error())
	}
	log.Printf("Creating stills...")
	err = cmd.Wait()
	if err != nil {
		log.Fatalf("Creating stills... FAILED [%s]", errB.String())
	}

	log.Printf("Creating stills... Done")
}

func makeCacheDir(filename string, overwrite bool) string {
	dir := filepath.Dir(filename)
	newDir := fmt.Sprintf("%s/~cache", dir)

	// remove the directory and it's contents if it exists
	if overwrite {
		os.RemoveAll(newDir)
	}

	// then create the directory
	err := os.Mkdir(newDir, 0755)
	if err != nil {
		log.Printf("Error creating directory: [%s]", err.Error())
		return newDir
	}

	return newDir
}

func removeCacheDir(path string) {
	// remove the directory and it's contents if it exists
	os.RemoveAll(path)
}

func walkDirectory(dir string, walkFunc WalkFunc) {
	files, err := ioutil.ReadDir(dir + "/")
	if err != nil {
		log.Fatal(err)
	}

	noOfFilesInDir := len(files)

	for i := 0; i < noOfFilesInDir; i += _THREADS_THRESHOLD {
		sem := make(chan done, _THREADS_THRESHOLD)

		for j := 0; j < _THREADS_THRESHOLD; j++ {
			if i+j < noOfFilesInDir {
				go func(dir string, f os.FileInfo) {
					walkFunc(dir+"/"+f.Name(), f, nil)
					sem <- done{}
				}(dir, files[i+j])
			} else {
				sem <- done{}
			}
		}

		for k := 0; k < _THREADS_THRESHOLD; k++ {
			<-sem
		}
	}
}

func analyseCredits(path string, info os.FileInfo, err error) error {

	if info.IsDir() || strings.Split(info.Name(), ".")[1] != "jpeg" {
		return nil
	}

	cmd := exec.Command("python", "scripts/label_image.py", "--graph=tf_files/retrained_graph.pb", "--image="+path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Printf("Analysing file %s...", path)
	err = cmd.Run()
	if err != nil {
		log.Printf("Analysing file %s... FAILED [%s]", path, stderr.String())
		return err
	}
	log.Printf("Analysing file %s... Done", path)

	analysis := AnalysisType{}
	if err := json.Unmarshal(stdout.Bytes(), &analysis); err != nil {
		log.Println(err)
		return err
	}

	var isCredits = "0"
	index, _ := strconv.Atoi(strings.Split(info.Name(), ".")[0])

	if analysis.Credits > 0.9 {
		_results[index] = 1
		isCredits = fmt.Sprintf("%d", _results[index])
	}

	file, err := os.OpenFile(_cacheDir+"/results.txt", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0660)
	if err != nil {
		log.Printf("Create analysis cache... FAIL [%s]", err)
		return err
	}

	defer file.Close()

	if _, err := file.WriteString(info.Name() + "-" + isCredits + "\n"); err != nil {
		log.Printf("Write analysis results... FAIL [%s]", err)
		return err
	}

	return nil
}

func getIndex() int {
	lenOfResults := len(_results)

	for i := 0; i < lenOfResults; i++ {
		sum := 0.0
		j := 0
		for ; j < _SAMPLE_SIZE; j++ {
			if i+j < lenOfResults {
				sum += float64(_results[i+j])
			}
		}
		if average := sum / float64(j+1); average > _MEAN_THRESHOLD {
			return i
		}
	}

	return 0
}

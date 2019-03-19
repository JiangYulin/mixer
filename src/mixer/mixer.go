package mixer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	"../generator"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type MixerAPI struct{}

const CACHEFOLDER = "/tmp/mixer/"

//根据实际机器配置
const FFMPEG = "ffmpeg"
const BASH = "/bin/sh"
const AUDIO_EXT = ".aac"
const OSS_PREFIX = "mixer/"

const BUCKET_NAME = "study-all-in"
const ENDPOINT = "oss-cn-beijing.aliyuncs.com"
const OSS_KEY = "LTAI3tmAj2frH3n2"
const OSS_SEC = "bxsoU8C4Ju4HTSqqyICnZxkVkRiNTC"

const STATUS_SUCCESS = "success"
const STATUS_FAIL = "fail"

type AudioObject struct {
	Record struct {
		Url      string `json:"url"`
		Path     string
		Duration float64
	} `json:"record"`

	Background struct {
		Url           string `json:"url"`
		Path          string
		Duration      float64
		FixedFilePath string //拼接或裁剪完的音频地址
	} `json:"background"`

	ConcatFileList string //ffmpeg 连接音频时需要使用的文件
	output         string
}

type ResponseMessage struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Path    string `json:"path"`
}

type MediaInfo struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (u *MixerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Got call from ", r.RemoteAddr)
	switch r.Method {
	case http.MethodPost:
		doPost(w, r)
	case http.MethodGet:
		doGet(w, r)
	default:
		fmt.Println("unsupported method")
	}

}
func PathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
func doPost(w http.ResponseWriter, r *http.Request) {

	isExist, os_err := PathExists(CACHEFOLDER)

	if os_err != nil {
		HandleError(os_err, w)
		return
	}
	if !isExist {
		os_err := os.Mkdir(CACHEFOLDER, 0700)
		if os_err != nil {
			HandleError(os_err, w)
			return
		}
	}

	b, err := ioutil.ReadAll(r.Body)

	defer r.Body.Close()
	var t AudioObject
	if err != nil {
		HandleError(err, w)
	}
	err = json.Unmarshal(b, &t)
	if err != nil {
		HandleError(err, w)
		return
	}

	//判断数组长度为2,只能合成一遍数据
	if t.Record.Url == "" {
		HandleError(errors.New("音频数量限定为2"), w)
		return
	}

	//下载录音
	new_filename := generator.RandStringRunes(16)
	t.Record.Path = CACHEFOLDER + new_filename
	//保存文件到本地
	err = DownloadFile(t.Record.Path, t.Record.Url)
	if err != nil {
		HandleError(err, w)
		clearFiles(t)
		return
	}
	//下载背景音乐
	new_filename = generator.RandStringRunes(16)
	t.Background.Path = CACHEFOLDER + new_filename + AUDIO_EXT
	//保存文件到本地
	err = DownloadFile(t.Background.Path, t.Background.Url)
	if err != nil {
		HandleError(err, w)
		clearFiles(t)
		return
	}

	// 判断音频长度，以决定是延长还是截断背景音乐
	// ffprobe -i short.m4a -v quiet -print_format json -show_format -show_streams -hide_banner
	// audio[0] 为录音、audio[1] 为背景
	// ffprobe -i short.m4a -v quiet -print_format json -show_format -show_streams -hide_banner

	t.Record.Duration = getAudioDuration(t.Record.Path)
	t.Background.Duration = getAudioDuration(t.Background.Path)

	if t.Record.Duration == -1 || t.Background.Duration == -1 {
		HandleError(errors.New("解析音频长度出错"), w)
		clearFiles(t)
		return
	}

	/**
	 * 背景音乐长度大于录音,直接截取背景音乐与录音相同
	 **/

	new_filename = generator.RandStringRunes(16)
	t.Background.FixedFilePath = CACHEFOLDER + new_filename + AUDIO_EXT
	if t.Record.Duration <= t.Background.Duration { //裁剪背景音乐

		ffmpeg_cut_command := FFMPEG + " -i " + t.Background.Path + " -t " + fmt.Sprintf("%.2f", t.Record.Duration) + " " + t.Background.FixedFilePath
		cmd := exec.Command(BASH, "-c", ffmpeg_cut_command)
		err = cmd.Run()
		if err != nil {
			HandleError(err, w)
			clearFiles(t)
			return
		}
	} else {

		x := int(math.Ceil(t.Record.Duration / t.Background.Duration))
		new_filename = generator.RandStringRunes(16)
		t.ConcatFileList = CACHEFOLDER + new_filename
		f, err := os.Create(t.ConcatFileList)

		if err != nil {
			HandleError(err, w)
			clearFiles(t)
			return
		}

		for i := 0; i < x; i++ {
			f.WriteString("file '" + t.Background.Path + "'\n")
		}

		defer f.Close()
		f.Close()

		//ffmpeg拼接音频
		ffmpeg_concat_command := FFMPEG + " -f concat -safe 0 -i " + t.ConcatFileList + " -c copy " + t.Background.FixedFilePath
		println(ffmpeg_concat_command)
		cmd := exec.Command(BASH, "-c", ffmpeg_concat_command)
		err = cmd.Run()
		if err != nil {
			HandleError(err, w)
			clearFiles(t)
			return
		}
	}

	output_filename := generator.RandStringRunes(16) + AUDIO_EXT
	t.output = CACHEFOLDER + output_filename
	ffmpeg_command := FFMPEG + " -i " + t.Record.Path + " -i " + t.Background.FixedFilePath + " -filter_complex amerge -y " + t.output
	log.Println("run merge command, this will take several seconds.")
	// log.Println(ffmpeg_command)
	//准备合成
	cmd := exec.Command(BASH, "-c", ffmpeg_command)
	err = cmd.Run()
	if err != nil {
		log.Fatalf("cmd.Run() failed with %s\n", err)
		HandleError(err, w)
		clearFiles(t)
		return
	}
	log.Println("merge complete!")
	log.Println("start uploading to remote server.")
	// 上传至alioss
	client, err := oss.New(ENDPOINT, OSS_KEY, OSS_SEC)
	if err != nil {
		HandleError(err, w)
		clearFiles(t)
		return
	}

	bucket, err := client.Bucket(BUCKET_NAME)
	if err != nil {
		HandleError(err, w)
		clearFiles(t)
		return
	}
	err = bucket.PutObjectFromFile(OSS_PREFIX+output_filename, t.output)
	if err != nil {
		HandleError(err, w)
		clearFiles(t)
		return
	} //上传结束
	log.Println("upload complete!")

	clearFiles(t)
	HandleSuccess(w, "https://"+BUCKET_NAME+"."+ENDPOINT+"/"+OSS_PREFIX+output_filename)
}

func HandleError(err error, w http.ResponseWriter) {
	println("failed!")
	w.Header().Set("content-type", "application/json")
	response := ResponseMessage{Status: STATUS_FAIL, Message: err.Error(), Path: ""}
	str, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "{\"status\":"+STATUS_FAIL+",\"message\":\"处理返回值时出错\"}", 500)
		return
	}
	fmt.Fprintf(w, string(str))
}

func clearFiles(obj AudioObject) {
	println("remove tmp files.")
	err := os.Remove(obj.Record.Path)
	if err != nil {
		println("remove file failed:", obj.Record.Path)
	}
	err = os.Remove(obj.Background.Path)
	if err != nil {
		println("remove file failed:", obj.Background.Path)
	}
	err = os.Remove(obj.output)
	if err != nil {
		println("remove file failed:", obj.output)
	}

}

func HandleSuccess(w http.ResponseWriter, path string) {
	println("success!")
	w.Header().Set("content-type", "application/json")
	response := ResponseMessage{Status: STATUS_SUCCESS, Message: "", Path: path}
	str, err := json.Marshal(response)
	if err != nil {
		HandleError(err, w)
		return
	}
	fmt.Fprintf(w, string(str))
}

func doGet(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "Unsupported request")
}

func DownloadFile(filepath string, url string) error {

	println("fetching file:", url)
	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		println(1)
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

func getAudioDuration(path string) float64 {
	// ffprobe -i short.m4a -v quiet -print_format json -show_format -show_streams -hide_banner

	ffmpeg_command := "/usr/bin/ffprobe -i " + path + " -v quiet -print_format json -show_format -show_streams -hide_banner"
	cmd := exec.Command(BASH, "-c", ffmpeg_command)
	out, err := cmd.Output()
	if err != nil {
		println(err.Error())
		return -1
	}

	var t MediaInfo
	err = json.Unmarshal(out, &t)
	if err == nil {
		i, _err := strconv.ParseFloat(t.Format.Duration, 64)
		if _err != nil {
			println(_err.Error())
			return -1
		}
		println(i)
		return i
	}
	return 0

}

func execCommand(commandName string, params []string) bool {
	cmd := exec.Command(commandName, params...)

	//显示运行的命令
	fmt.Println(cmd.Args)

	stdout, err := cmd.StdoutPipe()

	if err != nil {
		fmt.Println(err)
		return false
	}

	cmd.Start()

	reader := bufio.NewReader(stdout)

	//实时循环读取输出流中的一行内容
	for {
		line, err2 := reader.ReadString('\n')
		if err2 != nil || io.EOF == err2 {
			break
		}
		fmt.Println(line)
	}

	cmd.Wait()
	return true
}

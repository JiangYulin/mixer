package mixer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"

	"../generator"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type MixerAPI struct{}

const CACHEFOLDER = "../tmp/"

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

type AudioList struct {
	Audios   []string `json:"audios"`
	Str      string   `json:"str"`
	FilePath [2]string
	output   string
}

type ResponseMessage struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Path    string `json:"path"`
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

func doPost(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)

	defer r.Body.Close()
	var t AudioList
	if err != nil {
		HandleError(err, w)
	}
	err = json.Unmarshal(b, &t)
	if err != nil {
		HandleError(err, w)
		return
	}

	//判断数组长度为2,只能合成一遍数据
	if len(t.Audios) != 2 {
		HandleError(errors.New("音频数量限定为2"), w)
		return
	}

	//下载两个音频文件
	for i := 0; i < len(t.Audios); i++ {
		new_filename := generator.RandStringRunes(16)
		t.FilePath[i] = CACHEFOLDER + new_filename
		//保存文件到本地
		err = DownloadFile(t.FilePath[i], t.Audios[i])
		if err != nil {
			HandleError(errors.New("下载音频时出现错误"), w)
			clearFiles(t)
			return
		}
	}

	output_filename := generator.RandStringRunes(16) + AUDIO_EXT
	t.output = CACHEFOLDER + output_filename

	ffmpeg_command := FFMPEG + " -i " + t.FilePath[0] + " -i " + t.FilePath[1] + " -filter_complex amerge -y " + t.output
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

func clearFiles(obj AudioList) {
	println("remove tmp files.")
	err := os.Remove(obj.FilePath[0])
	if err != nil {
		println("remove file failed:", obj.FilePath[0])
	}
	err = os.Remove(obj.FilePath[1])
	if err != nil {
		println("remove file failed:", obj.FilePath[1])
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
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
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

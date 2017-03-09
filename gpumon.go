package main // GPU Monitor, feed data to influxdb
import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	urlquery "net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
)

//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// GPUUtilization stores GPU resource usage summary
type GPUUtilization struct {
	GPUUtil     string `xml:"gpu_util"`
	MemoryUtil  string `xml:"memory_util"`
	EncoderUtil string `xml:"encoder_util"`
	DecoderUtil string `xml:"decoder_util"`
}

// MemoryUsage shows total, used and free memory space
type MemoryUsage struct {
	Total string `xml:"total"`
	Used  string `xml:"used"`
	Free  string `xml:"free"`
}

// GPUInfo shows per GPU card spec and status
type GPUInfo struct {
	ID           string `xml:"id,attr"`
	ProductName  string `xml:"product_name"`
	ProductBrand string `xml:"product_brand"`
	UUID         string `xml:"uuid"`
	// Device Minor Number
	MinorNumber     int32          `xml:"minor_number"`
	FBMemoryUsage   MemoryUsage    `xml:"fb_memory_usage"`
	Bar1MemoryUsage MemoryUsage    `xml:"bar1_memory_usage"`
	Utilization     GPUUtilization `xml:"utilization"`
}

// NvidiaSmiLog describe nvidia-smi output
type NvidiaSmiLog struct {
	// Nvidia driver version
	DriverVersion string `xml:"driver_version"`
	// Attached GPU Count.
	AttachedGPUs string `xml:"attached_gpus"`
	// GPUinfo
	GPUInfoList []GPUInfo `xml:"gpu"`
}

func memUsage2Int(usage string) int64 {
	// convert string like 11519 MiB to bytes
	if strings.HasSuffix(usage, " MiB") {
		mega := strings.Replace(usage, " MiB", "", -1)
		megaInt, _ := strconv.ParseInt(mega, 10, 64)
		// FIXME: return parse error
		return megaInt * 1024 * 1024
	}
	return 0
}

func utilization2Float(utilization string) int64 {
	// convert string like 83 % to float point data
	if strings.HasSuffix(utilization, " %") {
		ut := strings.Replace(utilization, " %", "", -1)
		utInt, _ := strconv.ParseInt(ut, 10, 64)
		// FIXME: return parse error
		return utInt
	}
	return 0
}

func getGPUInfo() (*NvidiaSmiLog, error) {
	out, err := exec.Command("nvidia-smi", "-q", "-x").Output()
	if err != nil {
		return nil, err
	}
	nvidiasmilog := NvidiaSmiLog{}
	err = xml.Unmarshal([]byte(out), &nvidiasmilog)
	if err != nil {
		return nil, err
	}
	return &nvidiasmilog, err
}

func getURL(url string) (int, string) {
	resp, err := http.Get(url)
	if err != nil {
		glog.Errorf("url get error: %v", err)
		return -1, ""
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorf("parse url response error: %v", err)
		return -1, ""
	}
	glog.Infof("got response %s", body)
	return resp.StatusCode, string(body)
}

func postURL(url string, plainpost string) {
	glog.Infof("posting: %s", plainpost)
	resp, err := http.Post(url, "plain/text", strings.NewReader(plainpost))
	if err != nil {
		glog.Errorf("post error: %s", err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorf("error read response %s", err)
	}
	glog.Infof("response status[%d] %s", resp.StatusCode, body)
}

func appendPoint(output *bytes.Buffer, mesurement string, tags string, value int64, timestamp int64) {
	if output.Len() > 0 {
		output.WriteString("\n")
	}
	output.WriteString(fmt.Sprintf("%s,%s value=%d %d", mesurement, tags, value, timestamp))
}

func postToInfluxdb(xmlinfo *NvidiaSmiLog, baseurl string, hostname string, timestamp int64) {
	//create dababase if not exist
	var postbuffer bytes.Buffer
	url := baseurl + "query?q=CREATE%20DATABASE%20GPU"
	code, body := getURL(url)
	if code != 200 {
		glog.Errorf("create database faild, code %d, %s", code, body)
	}
	writeurl := baseurl + "write?db=GPU"
	for _, gpustat := range xmlinfo.GPUInfoList {
		postbuffer.Reset()
		tags := fmt.Sprintf("hostname=%s,gpuid=%s,product=%s,minor=%d",
			hostname, gpustat.ID, urlquery.QueryEscape(gpustat.ProductName), gpustat.MinorNumber)
		// FB
		appendPoint(&postbuffer, "fbmemory/total", tags, memUsage2Int(gpustat.FBMemoryUsage.Total), timestamp)
		appendPoint(&postbuffer, "fbmemory/used", tags, memUsage2Int(gpustat.FBMemoryUsage.Used), timestamp)
		appendPoint(&postbuffer, "fbmemory/free", tags, memUsage2Int(gpustat.FBMemoryUsage.Free), timestamp)
		// BAR1
		appendPoint(&postbuffer, "bar1memory/total", tags, memUsage2Int(gpustat.Bar1MemoryUsage.Total), timestamp)
		appendPoint(&postbuffer, "bar1memory/used", tags, memUsage2Int(gpustat.Bar1MemoryUsage.Used), timestamp)
		appendPoint(&postbuffer, "bar1memory/free", tags, memUsage2Int(gpustat.Bar1MemoryUsage.Free), timestamp)
		// Utilizations
		appendPoint(&postbuffer, "gpu", tags, utilization2Float(gpustat.Utilization.GPUUtil), timestamp)
		appendPoint(&postbuffer, "gpu/encoder", tags, utilization2Float(gpustat.Utilization.EncoderUtil), timestamp)
		appendPoint(&postbuffer, "gpu/decoder", tags, utilization2Float(gpustat.Utilization.DecoderUtil), timestamp)
		// Post these points to influxdb
		postURL(writeurl, postbuffer.String())
	}

}

func main() {
	hostname, _ := os.Hostname()
	influxdbAddr := os.Getenv("INFLUXDB_ADDR")
	flag.Parse()
	for {
		timestamp := time.Now().UnixNano()
		infos, err := getGPUInfo()
		if err != nil {
			glog.Errorf("get GPU info error: %v", err)
		}
		postToInfluxdb(infos, influxdbAddr, hostname, timestamp)
		time.Sleep(5 * time.Second)
	}

}

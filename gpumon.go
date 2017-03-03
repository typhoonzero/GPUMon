package main // GPU Monitor, feed data to influxdb
import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
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
	out, err := exec.Command("nvidia-smi -q -x").Output()
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
	return resp.StatusCode, string(body)
}

func postToInfluxdb(xmlinfo *NvidiaSmiLog, baseurl string, hostname string, timestamp int64) {
	//create dababase if not exist
	url := baseurl + "query?q=CREATE%20DATABASE%20GPU"
	code, body := getURL(url)
	if code != 200 {
		glog.Errorf("create database faild, code %d, %s", code, body)
	}
	writeurl := baseurl + "write?db=GPU"
	for _, gpustat := range xmlinfo.GPUInfoList {
		//-------------------------------------- FB --------------------------------------
		// FB memory total
		args := fmt.Sprintf("fbmemory/total,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, memUsage2Int(gpustat.FBMemoryUsage.Total), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))
		// FB memory used
		args = fmt.Sprintf("fbmemory/used,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, memUsage2Int(gpustat.FBMemoryUsage.Used), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))
		// FB memroy free
		args = fmt.Sprintf("fbmemory/free,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, memUsage2Int(gpustat.FBMemoryUsage.Free), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))
		//-------------------------------------- Bar1 --------------------------------------
		// Bar1 memory total
		args = fmt.Sprintf("bar1memory/total,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, memUsage2Int(gpustat.Bar1MemoryUsage.Total), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))
		// Bar1 memory used
		args = fmt.Sprintf("bar1memory/used,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, memUsage2Int(gpustat.Bar1MemoryUsage.Used), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))
		// Bar1 memory free
		args = fmt.Sprintf("bar1memory/free,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, memUsage2Int(gpustat.Bar1MemoryUsage.Free), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))

		//-------------------------------------- Utilization --------------------------------------
		// GPU Utilization
		args = fmt.Sprintf("gpu,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, utilization2Float(gpustat.Utilization.GPUUtil), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))
		// FIXME: memory util not needed?
		// GPU Encoder
		args = fmt.Sprintf("gpu/encoder,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, utilization2Float(gpustat.Utilization.EncoderUtil), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))
		// GPU Decoder
		args = fmt.Sprintf("gpu/decoder,hostname=%s,gpuid=%s,product=%s,minor=%d value=%d %d",
			hostname, gpustat.ID, gpustat.ProductName, gpustat.MinorNumber, utilization2Float(gpustat.Utilization.DecoderUtil), timestamp)
		http.Post(writeurl, "plain/text", strings.NewReader(args))

	}

}

func main() {
	hostname, _ := os.Hostname()
	timestamp := time.Now().Unix() * 1000 * 1000
	for {
		infos, err := getGPUInfo()
		if err != nil {
			glog.Errorf("get GPU info error: %v", err)
		}
		postToInfluxdb(infos, "http://monitoring-influxdb:8086/", hostname, timestamp)
		time.Sleep(5 * time.Second)
	}

}

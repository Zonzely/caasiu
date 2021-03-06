package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/k0kubun/go-ansi"
	"github.com/schollz/progressbar/v3"
)

type Downloader struct {
	url           string
	blockSize     int
	concurrencyN  int
	filepath      string
	client        *http.Client
	contentLength int64
	bar           *progressbar.ProgressBar
}

func (d *Downloader) generateFilepath(inputFilepath, headerFilename string) string {
	dirpath := ""
	isDir := false
	if file, err := os.Stat(inputFilepath); err == nil && file.IsDir() {
		dirpath = inputFilepath
		isDir = true
	}

	filename := strings.Split(path.Base(d.url), "?")[0]
	if headerFilename != "" {
		filename = headerFilename
	}
	if !isDir && inputFilepath != "" {
		filename = inputFilepath
	}
	return path.Join(dirpath, filename)
}

func (d *Downloader) calcDownloadedSize() ([]int, int) {
	downloadedSizeList := make([]int, d.concurrencyN)
	totalDownloadSize := 0
	for i := 0; i < d.concurrencyN; i++ {
		filepath := fmt.Sprintf("%v-%v", d.filepath, i)

		if fileInfo, err := os.Stat(filepath); err != nil {
			downloadedSizeList[i] = 0
		} else {
			downloadedSizeList[i] = int(fileInfo.Size())
		}
		totalDownloadSize += downloadedSizeList[i]
	}
	return downloadedSizeList, totalDownloadSize
}

func (d *Downloader) Download() {
	log.Printf("url: %v\n", d.url)

	//  send heading
	req, err := http.NewRequest(http.MethodHead, d.url, nil)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	log.Println("finish heading")

	// set the max size
	d.contentLength = resp.ContentLength

	// set the number of concurrency
	acceptRanges := resp.Header.Get("Accept-Ranges")
	if acceptRanges != "bytes" {
		d.concurrencyN = 1
		log.Printf("%v, partial request is not supported, reset concurrencyN=1", d.url)
	}

	// set filepath
	headerFilename := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		headerFilename = params["filename"]
	}
	d.filepath = d.generateFilepath(d.filepath, headerFilename)

	// set progressbar
	d.bar = progressbar.NewOptions(
		int(d.contentLength),
		progressbar.OptionSetWriter(ansi.NewAnsiStdout()),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetDescription("downloading..."),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	downloadedSizeList, totalDownloadSize := d.calcDownloadedSize()
	if err := d.bar.Set(totalDownloadSize); err != nil {
		log.Fatal(err)
	}

	// set partSize
	var downloaderWg sync.WaitGroup
	downloaderWg.Add(d.concurrencyN)

	var writerWg sync.WaitGroup
	writerWg.Add(1)
	partSize := int(d.contentLength) / d.concurrencyN

	file, err := os.Create(d.filepath)
	if err != nil {
		log.Fatal(err)
	}

	// download
	for i := 0; i < d.concurrencyN; i++ {
		rangeStart := i*partSize + downloadedSizeList[i]
		rangeEnd := i*partSize + partSize - 1
		if i == d.concurrencyN-1 {
			rangeEnd = int(d.contentLength)
		}

		go httpDownload(d.client, d.url, rangeStart, rangeEnd, &downloaderWg, file, d.bar)
	}

	downloaderWg.Wait()
	log.Println("finished!")
}

func httpDownload(
	client *http.Client,
	url string,
	rangeStart, rangeEnd int,
	wg *sync.WaitGroup,
	file *os.File,
	bar *progressbar.ProgressBar) {
	log.Printf("start downloading, rangeStart: %d, rangeEnd: %d\n", rangeStart, rangeEnd)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%v-%v", rangeStart, rangeEnd))

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	defer wg.Done()

	var buf bytes.Buffer

	if _, err := io.Copy(io.MultiWriter(&buf, bar), resp.Body); err != nil && err != io.EOF {
		log.Fatal(err)
	}

	if _, err := file.WriteAt(buf.Bytes(), int64(rangeStart)); err != nil {
		log.Fatal(err)
	}

}

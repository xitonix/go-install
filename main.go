package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cavaliercoder/grab"
	"github.com/gocolly/colly/v2"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	base = "https://golang.org/dl"
)

// Version build flags
var (
	version    string
)


func main() {
	log.SetFlags(0)
	app := kingpin.New("go-install", "A CLI tool to install/update the latest Go binaries on your machine.")
	root := app.Flag("go-base", "The root path to install the runtime. Go will be installed in `go-base/go`.").
		Envar("GO_BASE").
		Short('g').
		Required().
		String()

	app.Command("version", "Displays the current version of the tool.").Action(printVersion)

	cmd, err := app.Parse(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	if cmd == "version" {
		return
	}

	suffix := fmt.Sprintf("%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)

	var url string
	c := colly.NewCollector()
	c.MaxDepth = 1
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		if len(url) > 0 {
			return
		}
		href := e.Attr("href")
		if strings.Contains(href, suffix) {
			url = "https://golang.org" + href
		}
	})

	log.Printf("Looking for the latest %s release on the server.", suffix)
	err = c.Visit(base)
	if err != nil {
		log.Fatal(err)
	}

	if len(url) == 0 {
		log.Fatalf("%s file was not found on the server!", suffix)
	}

	newVersion, currentVersion := checkVersions(url)

	msg := fmt.Sprintf("Latest: v%s", newVersion)
	if currentVersion != "" {
		msg = fmt.Sprintf("Installed: v%s, ", currentVersion) + msg
	}

	if !askForConfirmation(msg + " Would you like to proceed") {
		return
	}

	log.Printf("Preparing to install v%s", newVersion)

	tarFile, err := downloadFile(url)
	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		cleanup(tarFile)
		current := getCurrentVersion()
		fmt.Println(strings.TrimSpace(current))
	}()

	err = install(newVersion, currentVersion, tarFile, *root)
	if err != nil {
		log.Fatal(err)
	}
}

func printVersion(*kingpin.ParseContext) error {
	if version == "" {
		version = "[built from source]"
	}
	fmt.Printf("go-install v%s", version)
	return nil
}

func install(newVersion, currentVersion, downloadedTar, root string) error {
	if currentVersion != "" {
		err := removeCurrentVersion(currentVersion, root)
		if err != nil {
			return err
		}
	}
	log.Printf("Installing v%s runtime", newVersion)

	return extract(downloadedTar, root)
}

func extract(tarName, destinationDir string) (err error) {
	tarFile, err := os.Open(tarName)
	if err != nil {
		return err
	}
	defer func() {
		err := tarFile.Close()
		if err != nil {
			log.Printf("Failed to close the input tar file: %s", err)
		}
	}()

	absPath, err := filepath.Abs(destinationDir)
	if err != nil {
		return err
	}

	gz, err := gzip.NewReader(tarFile)
	if err != nil {
		return err
	}

	defer func() {
		err := gz.Close()
		if err != nil {
			log.Printf("Failed to close the gzip reader: %s", err)
		}
	}()
	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// determine proper file path info
		fileInfo := hdr.FileInfo()
		fileName := hdr.Name
		absFileName := filepath.Join(absPath, fileName)
		// if a dir, create it, then go to next segment
		if fileInfo.Mode().IsDir() {
			if err := os.MkdirAll(absFileName, 0755); err != nil {
				return err
			}
			continue
		}
		// create new file with original file mode
		file, err := os.OpenFile(
			absFileName,
			os.O_RDWR|os.O_CREATE|os.O_TRUNC,
			fileInfo.Mode().Perm(),
		)
		if err != nil {
			return err
		}
		log.Printf("Extracting %s", absFileName)
		n, cpErr := io.Copy(file, tr)
		if closeErr := file.Close(); closeErr != nil {
			return err
		}
		if cpErr != nil {
			return cpErr
		}
		if n != fileInfo.Size() {
			return fmt.Errorf("file size mismatch. Wrote %d, Wanted %d", n, fileInfo.Size())
		}
	}
	return nil
}

func removeCurrentVersion(currentVersion string, root string) error {
	log.Printf("Removing v%s files", currentVersion)
	currentPath := path.Join(root, "go")
	err := os.RemoveAll(currentPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove %s: %w", currentPath, err)
	}
	return nil
}

func checkVersions(url string) (string, string) {
	current := getCurrentVersion()
	reg := regexp.MustCompile(`\d+\.\d+\.\d+`)
	return reg.FindString(url), strings.TrimSpace(reg.FindString(current))
}

func getCurrentVersion() string {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		log.Printf("Could not find current installation: %s", err)
		return ""
	}
	return string(out)
}

func printDownloadPercent(wg *sync.WaitGroup, resp *grab.Response) {
	t := time.NewTicker(1 * time.Second)
	defer func() {
		t.Stop()
		wg.Done()
	}()

	for {
		select {
		case <-t.C:
			fmt.Printf("Transferred %v/%v bytes (%.2f%%)       \r",
				resp.BytesComplete(),
				resp.Size,
				100*resp.Progress())

		case <-resp.Done:
			fmt.Printf("Transferred %v/%v bytes (%.2f%%)       \n",
				resp.BytesComplete(),
				resp.Size,
				100*resp.Progress())
			return
		}
	}
}

func downloadFile(url string) (string, error) {
	file := path.Base(url)
	dest := path.Join(os.TempDir(), file)
	log.Printf("Downloading %s to %s", file, dest)
	fmt.Println(url)
	client := grab.NewClient()
	req, err := grab.NewRequest(dest, url)
	if err != nil {
		return "", fmt.Errorf("failed to initialise the download request: %w", err)
	}
	req.NoResume = true
	resp := client.Do(req)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go printDownloadPercent(wg, resp)
	if err := resp.Err(); err != nil {
		return "", err
	}
	wg.Wait()
	return dest, nil
}

func cleanup(filePath string) {
	log.Printf("Removing %s", filePath)
	err := os.Remove(filePath)
	if err != nil {
		log.Printf("Failed to remove the file: %s", err)
	}
}

func askForConfirmation(s string) bool {
	scanner := bufio.NewScanner(os.Stdin)
	msg := fmt.Sprintf("%s [y/n]?: ", s)
	for fmt.Print(msg); scanner.Scan(); fmt.Print(msg) {
		r := strings.ToLower(strings.TrimSpace(scanner.Text()))
		switch r {
		case "y", "yes":
			return true
		case "n", "no", "q", "quit", "exit":
			return false
		}
	}
	return false
}

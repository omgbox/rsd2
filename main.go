package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/anacrolix/torrent"
	"github.com/google/uuid"
)

var (
	progressMap     = make(map[string]*ProgressResponse)
	downloadMap     = make(map[string]chan bool)
	fileMap         = make(map[string]string) // Map to store the file path for each session
	mu              sync.Mutex
	bufferPool      = sync.Pool{
		New: func() interface{} {
			return make([]byte, 1024)
		},
	}
	users = map[string]string{
		"demo": "password",
		"downloads": "downloads",
		// Add more users as needed
	}
)

type ProgressResponse struct {
	Progress        int    `json:"progress"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalSizeBytes  int64  `json:"total_size_bytes"`
}

func downloadTorrent(magnetURI string, cancelChan chan bool, progress *ProgressResponse, sessionID string, downloadDir string) error {
	clientConfig := torrent.NewDefaultClientConfig()
	clientConfig.DataDir = downloadDir
	clientConfig.ListenPort = 0 // Allow the client to choose an available port

	client, err := torrent.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create torrent client: %w", err)
	}
	defer client.Close()

	t, err := client.AddMagnet(magnetURI)
	if err != nil {
		return fmt.Errorf("failed to add magnet URI: %w", err)
	}

	<-t.GotInfo()

	// Calculate the total size of the torrent
	var totalSize int64
	for _, file := range t.Files() {
		totalSize += file.Length()
	}
	progress.TotalSizeBytes = totalSize

	// Download all files in the torrent
	for _, file := range t.Files() {
		err := downloadFile(file, cancelChan, progress, sessionID, downloadDir)
		if err != nil {
			return err
		}
	}

	return nil
}

func downloadFile(file *torrent.File, cancelChan chan bool, progress *ProgressResponse, sessionID string, downloadDir string) error {
	filePath := filepath.Join(downloadDir, file.Path())
	fileMap[sessionID] = filePath

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	outFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer outFile.Close()

	reader := file.NewReader()
	defer reader.Close()

	buffer := bufferPool.Get().([]byte)
	defer bufferPool.Put(buffer)

	for {
		select {
		case <-cancelChan:
			// Delete the file when canceled
			if err := os.Remove(filePath); err != nil {
				log.Printf("Error deleting file: %v", err)
			}
			return nil
		default:
			n, err := reader.Read(buffer)
			if n > 0 {
				progress.DownloadedBytes += int64(n)
				progress.Progress = int(float64(progress.DownloadedBytes) / float64(progress.TotalSizeBytes) * 100)
				_, err := outFile.Write(buffer[:n])
				if err != nil {
					return fmt.Errorf("failed to write to file: %w", err)
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed to read from torrent file: %w", err)
			}
		}
	}

	return nil
}

func progressHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionID")
	if sessionID == "" {
		http.Error(w, "sessionID is required", http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	progress, exists := progressMap[sessionID]
	if !exists {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(progress)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	html := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Torrent Downloader</title>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<style>
			body {
				display: flex;
				justify-content: center;
				align-items: center;
				height: 100vh;
				font-family: Arial, sans-serif;
				background-color: #f0f0f0;
				margin: 0;
			}
			.container {
				text-align: center;
				background-color: #fff;
				padding: 20px;
				border-radius: 10px;
				box-shadow: 0 0 10px rgba(0, 0, 0, 0.1);
			}
			input[type="text"] {
				width: 100%;
				padding: 10px;
				margin-bottom: 10px;
				border: 1px solid #ccc;
				border-radius: 5px;
			}
			button {
				padding: 10px 20px;
				background-color: #007bff;
				color: #fff;
				border: none;
				border-radius: 5px;
				cursor: pointer;
				margin-top: 10px;
			}
			button:hover {
				background-color: #0056b3;
			}
			progress {
				width: 100%;
				height: 20px;
				margin-top: 20px;
			}
			#cancelBtn {
				display: none;
				margin-top: 10px;
			}
			.error-message {
				color: red;
				margin-top: 10px;
			}
		</style>
		<script>
			var sessionID = "` + uuid.New().String() + `";
			var cancelDownload = null;

			function formatBytes(bytes) {
				return (bytes / (1024 * 1024)).toFixed(2) + " MB";
			}

			function updateProgress() {
				var xhr = new XMLHttpRequest();
				xhr.open("GET", "/progress?sessionID=" + sessionID, true);
				xhr.onreadystatechange = function() {
					if (xhr.readyState == 4 && xhr.status == 200) {
						var response = JSON.parse(xhr.responseText);
						document.getElementById("progressBar").value = response.progress;
						document.getElementById("downloaded").innerText = formatBytes(response.downloaded_bytes);
						document.getElementById("totalSize").innerText = formatBytes(response.total_size_bytes);
						if (response.progress < 100 && cancelDownload !== null) {
							setTimeout(updateProgress, 100);
						} else {
							document.getElementById("downloadBtn").style.display = "inline";
							document.getElementById("cancelBtn").style.display = "none";
							cancelDownload = null;
						}
					}
				};
				xhr.send();
			}

			function startDownload() {
				// Reset the progress bar and related elements
				document.getElementById("progressBar").value = 0;
				document.getElementById("downloaded").innerText = "0 MB";
				document.getElementById("totalSize").innerText = "0 MB";
				document.getElementById("errorMessage").innerText = "";

				var magnetURI = document.getElementById("urlInput").value;
				var xhr = new XMLHttpRequest();
				xhr.open("POST", "/download?sessionID=" + sessionID, true);
				xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
				xhr.onreadystatechange = function() {
					if (xhr.readyState == 4) {
						if (xhr.status == 200) {
							document.getElementById("downloadBtn").style.display = "none";
							document.getElementById("cancelBtn").style.display = "inline";
							cancelDownload = function() {
								var cancelXhr = new XMLHttpRequest();
								cancelXhr.open("POST", "/cancel?sessionID=" + sessionID, true);
								cancelXhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
								cancelXhr.onreadystatechange = function() {
									if (cancelXhr.readyState == 4 && cancelXhr.status == 200) {
										document.getElementById("downloadBtn").style.display = "inline";
										document.getElementById("cancelBtn").style.display = "none";
										cancelDownload = null;
										updateProgress();
									}
								};
								cancelXhr.send();
							};
							updateProgress();
						} else {
							document.getElementById("errorMessage").innerText = "Error downloading torrent: " + xhr.responseText;
						}
					}
				};
				xhr.send("magnetURI=" + encodeURIComponent(magnetURI));
			}

			function cancelDownloadFunc() {
				if (cancelDownload !== null) {
					cancelDownload();
					// Reset the progress bar and related elements
					document.getElementById("progressBar").value = 0;
					document.getElementById("downloaded").innerText = "0 MB";
					document.getElementById("totalSize").innerText = "0 MB";
					document.getElementById("errorMessage").innerText = "";
				}
			}

			window.onload = function() {
				updateProgress();
			};
		</script>
	</head>
	<body>
		<div class="container">
			<h1>Torrent Downloader</h1>
			<input type="text" id="urlInput" placeholder="Enter Magnet URI to download">
			<button id="downloadBtn" onclick="startDownload()">Download</button>
			<button id="cancelBtn" onclick="cancelDownloadFunc()">Cancel</button>
			<h2>Download Progress</h2>
			<progress id="progressBar" value="0" max="100"></progress>
			<p>Downloaded: <span id="downloaded">0 MB</span></p>
			<p>Total Size: <span id="totalSize">0 MB</span></p>
			<p id="errorMessage" class="error-message"></p>
		</div>
	</body>
	</html>
	`
	fmt.Fprintf(w, html)
}

func downloadHandler(w http.ResponseWriter, r *http.Request, downloadDir string) {
	r.ParseForm()
	sessionID := r.URL.Query().Get("sessionID")
	if sessionID == "" {
		http.Error(w, "sessionID is required", http.StatusBadRequest)
		return
	}

	magnetURI := r.FormValue("magnetURI")

	mu.Lock()
	defer mu.Unlock()

	if _, exists := progressMap[sessionID]; exists {
		// Reset the state if a download is already in progress
		delete(progressMap, sessionID)
		delete(downloadMap, sessionID)
		delete(fileMap, sessionID)
	}

	progress := &ProgressResponse{
		Progress:        0,
		DownloadedBytes: 0,
		TotalSizeBytes:  0,
	}
	progressMap[sessionID] = progress
	cancelChan := make(chan bool)
	downloadMap[sessionID] = cancelChan

	go func() {
		err := downloadTorrent(magnetURI, cancelChan, progress, sessionID, downloadDir)
		if err != nil {
			log.Printf("Error downloading torrent: %v", err)
		} else {
			log.Println("Torrent downloaded successfully")
		}

		mu.Lock()
		defer mu.Unlock()
		delete(progressMap, sessionID)
		delete(downloadMap, sessionID)
		delete(fileMap, sessionID)
	}()

	w.WriteHeader(http.StatusOK)
}

func cancelHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionID")
	if sessionID == "" {
		http.Error(w, "sessionID is required", http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	cancelChan, exists := downloadMap[sessionID]
	if !exists {
		http.Error(w, "no download in progress for this session", http.StatusNotFound)
		return
	}

	// Signal the download goroutine to cancel
	cancelChan <- true

	// Delete the file and reset the state
	if filePath, exists := fileMap[sessionID]; exists {
		if err := os.Remove(filePath); err != nil {
			log.Printf("Error deleting file: %v", err)
		}
		delete(fileMap, sessionID)
	}

	// Reset the progress state
	progressMap[sessionID] = &ProgressResponse{
		Progress:        0,
		DownloadedBytes: 0,
		TotalSizeBytes:  0,
	}

	w.WriteHeader(http.StatusOK)
}

func basicAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || users[user] != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="Please enter your username and password."`)
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, "Unauthorized")
			return
		}
		handler(w, r)
	}
}

func main() {
	var downloadDir string
	var port int

	flag.StringVar(&downloadDir, "dir", ".", "Download directory")
	flag.IntVar(&port, "port", 8080, "Server port")
	flag.Parse()

	http.HandleFunc("/", basicAuth(indexHandler))
	http.HandleFunc("/progress", basicAuth(progressHandler))
	http.HandleFunc("/download", basicAuth(func(w http.ResponseWriter, r *http.Request) {
		downloadHandler(w, r, downloadDir)
	}))
	http.HandleFunc("/cancel", basicAuth(cancelHandler))

	log.Printf("Server started at http://localhost:%d", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

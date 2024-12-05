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
	"strings"
	"sync"

	"github.com/anacrolix/torrent"
	"github.com/google/uuid"
)

var (
	progressMap     = make(map[string]*ProgressResponse)
	downloadMap     = make(map[string]chan bool)
	fileMap         = make(map[string]string) // Map to store the file path for each session
	completedFiles  = make(map[string]string) // Map to store completed file paths
	mu              sync.Mutex
	bufferPool      = sync.Pool{
		New: func() interface{} {
			return make([]byte, 1024)
		},
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

	// Mark the session as completed
	mu.Lock()
	completedFiles[sessionID] = fileMap[sessionID]
	log.Printf("Completed file added: %s", fileMap[sessionID]) // Debugging log
	mu.Unlock()

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

	log.Printf("Attempting to create file: %s", filePath) // Debugging log

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
	<html lang="en">
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>Torrent Downloader</title>
		<link href="https://cdn.jsdelivr.net/npm/tailwindcss@2.2.19/dist/tailwind.min.css" rel="stylesheet">
		<link href="https://cdnjs.cloudflare.com/ajax/libs/flowbite/1.0.0/flowbite.min.css" rel="stylesheet">
		<style>
			.hidden {
				display: none;
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
						} else if (response.progress == 100) {
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

			function showFiles() {
				var xhr = new XMLHttpRequest();
				xhr.open("GET", "/files", true);
				xhr.onreadystatechange = function() {
					if (xhr.readyState == 4 && xhr.status == 200) {
						var response = JSON.parse(xhr.responseText);
						var filesContainer = document.getElementById("filesContainer");
						filesContainer.innerHTML = ` + "`" + `
							<div class="relative overflow-x-auto shadow-md sm:rounded-lg">
								<table class="w-full text-sm text-left rtl:text-right text-gray-500 dark:text-gray-400">
									<thead class="text-xs text-gray-700 uppercase bg-gray-50 dark:bg-gray-700 dark:text-gray-400">
										<tr>
											<th scope="col" class="px-6 py-3">
												File Name
											</th>
											<th scope="col" class="px-6 py-3">
												Action
											</th>
										</tr>
									</thead>
									<tbody>
										${response.map(file => ` + "`" + `
											<tr class="bg-white border-b dark:bg-gray-800 dark:border-gray-700">
												<th scope="row" class="px-6 py-4 font-medium text-gray-900 whitespace-nowrap dark:text-white">
													${file}
												</th>
												<td class="px-6 py-4">
													<a href="/download/${file}" class="font-medium text-blue-600 dark:text-blue-500 hover:underline">Download</a>
												</td>
											</tr>
										` + "`" + `).join('')}
									</tbody>
								</table>
							</div>
						` + "`" + `;
					}
				};
				xhr.send();
			}

			function toggleFiles() {
				var filesContainer = document.getElementById("filesContainer");
				if (filesContainer.classList.contains("hidden")) {
					filesContainer.classList.remove("hidden");
				} else {
					filesContainer.classList.add("hidden");
				}
			}

			window.onload = function() {
				updateProgress();
				showFiles();
				setInterval(showFiles, 5000); // Refresh files every 5 seconds
			};
		</script>
	</head>
	<body class="flex justify-center items-center h-screen bg-gray-100 p-8">
		<div class="container bg-white p-8 rounded-lg shadow-lg relative">
			<h1 class="text-3xl font-bold mb-6">Torrent Downloader</h1>
			<input type="text" id="urlInput" placeholder="Enter Magnet URI to download" class="w-full p-2 mb-4 border border-gray-300 rounded">
			<div class="flex justify-between items-center mb-6">
				<button id="cancelBtn" onclick="cancelDownloadFunc()" class="bg-red-500 text-white px-4 py-2 rounded" style="display:none;">Cancel</button>
				<button id="downloadBtn" onclick="startDownload()" class="bg-blue-500 text-white px-4 py-2 rounded">Download</button>
			</div>
			<div class="flex justify-end mt-4 mb-4">
				<button id="toggleFilesBtn" onclick="toggleFiles()" class="bg-blue-500 text-white px-4 py-2 rounded">Show Files</button>
			</div>
			<div id="progressTab" class="p-4 border border-t-0 rounded-b-lg">
				<h2 class="text-2xl font-bold mb-4">Download Progress</h2>
				<progress id="progressBar" value="0" max="100" class="w-full h-4 mb-4"></progress>
				<p>Downloaded: <span id="downloaded">0 MB</span></p>
				<p>Total Size: <span id="totalSize">0 MB</span></p>
				<p id="errorMessage" class="text-red-500"></p>
			</div>
			<div id="filesContainer" class="hidden p-4 border border-t-0 rounded-b-lg mt-4">
				<div class="relative overflow-x-auto shadow-md sm:rounded-lg">
					<table class="w-full text-sm text-left rtl:text-right text-gray-500 dark:text-gray-400">
						<thead class="text-xs text-gray-700 uppercase bg-gray-50 dark:bg-gray-700 dark:text-gray-400">
							<tr>
								<th scope="col" class="px-6 py-3">
									File Name
								</th>
								<th scope="col" class="px-6 py-3">
									Action
								</th>
							</tr>
						</thead>
						<tbody>
							<!-- Files will be populated here -->
						</tbody>
					</table>
				</div>
			</div>
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

func completedHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(completedFiles)
}

func downloadCompletedHandler(w http.ResponseWriter, r *http.Request, downloadDir string) {
	fileName := r.URL.Path[len("/download/"):]
	filePath := filepath.Join(downloadDir, fileName)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	http.ServeFile(w, r, filePath)
}

func filesHandler(w http.ResponseWriter, r *http.Request, downloadDir string) {
	var videoFiles []string

	err := filepath.Walk(downloadDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext == ".mkv" || ext == ".mp4" {
				relPath, err := filepath.Rel(downloadDir, path)
				if err != nil {
					return err
				}
				videoFiles = append(videoFiles, relPath)
			}
		}
		return nil
	})

	if err != nil {
		http.Error(w, "failed to read directory", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(videoFiles)
}

func main() {
	var downloadDir string
	var port int

	flag.StringVar(&downloadDir, "dir", ".", "Download directory")
	flag.IntVar(&port, "port", 8080, "Server port")
	flag.Parse()

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/progress", progressHandler)
	http.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		downloadHandler(w, r, downloadDir)
	})
	http.HandleFunc("/cancel", cancelHandler)
	http.HandleFunc("/completed", completedHandler)
	http.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		downloadCompletedHandler(w, r, downloadDir)
	})
	http.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		filesHandler(w, r, downloadDir)
	})

	log.Printf("Server started at http://localhost:%d", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

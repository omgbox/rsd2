Features of the Torrent Downloader App

# 1. Torrent Downloading
Efficient Downloading: Downloads torrents from magnet URIs.
Download Files from the UI 

# 2. Progress Tracking
Real-Time Monitoring: Tracks download progress in real-time.
Metrics: Provides progress percentage, downloaded bytes, and total size.

# 3. Cancellation
User Control: Allows users to cancel ongoing downloads.
Cleanup: Deletes partially downloaded files upon cancellation.

# 4. Basic Authentication
Secure Access: Requires username and password for access.
Dynamic User Management: Supports adding users via command-line flags.

# 5. Web Interface
User-Friendly: Provides a simple web interface for users to input magnet URIs and monitor download progress.
How to Use the Torrent Downloader App

# 1. Start the Server
Run the application with the following command:

you can start with default settings ./rsd2


./rsd2 --dir=/path/to/download/dir --port=8080 --user1=alice --pass1=secret1 --user2=bob --pass2=secret2
--dir: Specifies the download directory.
--port: Specifies the server port.
--user1, --pass1, --user2, --pass2: Specifies usernames and passwords for authentication.

# 2. Access the Web Interface
Open a web browser and navigate to:
http://localhost:8080
You will be prompted to enter your username and password.

# 3. Download a Torrent
Enter Magnet URI:
In the web interface, enter the magnet URI of the torrent you want to download.
Start Download:
Click the "Download" button to start the download.
Monitor Progress:
The progress bar will update in real-time, showing the download percentage, downloaded bytes, and total size.
Cancel Download:
If needed, click the "Cancel" button to stop the download and delete the partially downloaded file.

# 4. Check Downloaded Files
Once the download is complete, the files will be saved in the specified download directory.

Summary
Start the server with the desired download directory, port, and user credentials.
Access the web interface via a browser.
Enter a magnet URI to start downloading.
Monitor and manage downloads through the web interface.
This app provides a simple and secure way to download torrents with real-time progress tracking and cancellation capabilities.
 

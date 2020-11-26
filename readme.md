This is a Go program that normalizes audio files, i.e. make them all have roughly the same loudness.
This program relies on ffmpeg being installed and available in the PATH.


Installation
------------

Install Go:

	https://golang.org/dl/


Download ffmpeg:

	https://ffmpeg.org/download.html

Place ffmpeg in your PATH, i.e. make it available from your command line.


Install this program via go get:

	go get -u github.com/gonutz/normalize

where the -u option is to get the latest version online on Github.


Usage
-----

	normalize -ampl=1400 -proc=8 "path/to/file.mp3" "folder/of/mp3s/"

Pass the options first:

	-ampl int
		Determines the amplitude. Increase this value to make songs louder. (default 1400)
	-proc int
		Processes to start in parallel. Adjust this value so your CPU does not catch fire. (default 8)

Both are optional.

After the options, pass your files and folders.
Each path can be either a file which is then normalized or a folder.
From each given folder all MP3 files will be normalized.
If you pass no path at all, all MP3 files in the current working directory are normalized.

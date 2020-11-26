package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// scaleFactor can be tweaked to control how loud your files will overall
	// be. Look at the output for an example file and make it twice as large if
	// you want to. The original value was chosen to make mp3s have the right
	// loudness for the radio in my car.
	scaleFactor = 1400

	// parallel determines how many conversions happen in parallel. If you run
	// out of memory or your CPU catches fire, this might be set too high.
	parallel = 8
)

func main() {
	// User is expected to pass:
	// - the path to a single sound file or
	// - the path to a directory in which all mp3 files will be converted or
	// - nothing, in this case all mp3 files in the current directory are
	//   converted.
	files, err := readFilesFromArgs(os.Args[1:])
	if err != nil {
		panic(err)
	}

	// We will write WAV files to a temporary folder in the process.
	tempWavDir, err := ioutil.TempDir("", "normalize")
	if err != nil {
		tempWavDir = "."
	} else {
		defer os.Remove(tempWavDir)
	}

	var wg sync.WaitGroup
	paths := make(chan string)

	for i := 0; i < parallel; i++ {
		go func() {
			for {
				path := <-paths
				err := normalizeFile(path, tempWavDir)
				if err != nil {
					fmt.Println("ERROR", path, err)
				}
				wg.Done()
			}
		}()
	}

	for _, file := range files {
		if strings.HasSuffix(strings.ToLower(file), ".mp3") {
			wg.Add(1)
			paths <- file
		}
	}

	wg.Wait()
}

func readFilesFromArgs(args []string) ([]string, error) {
	var path string
	if len(os.Args) == 1 {
		path = "."
	} else if len(os.Args) == 2 {
		path = os.Args[1]
	} else {
		return nil, errors.New("wrong arguments, give none or one path")
	}

	pathInfo, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var files []string
	if !pathInfo.IsDir() {
		files = []string{path}
	} else {
		all, err := ioutil.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, f := range all {
			if !f.IsDir() {
				files = append(files, filepath.Join(path, f.Name()))
			}
		}
	}

	return files, nil
}

func normalizeFile(path, tempDir string) error {
	fileName := filepath.Base(path)
	wavPath := filepath.Join(tempDir, fileName+".temp.wav")
	defer os.Remove(wavPath)

	if err := toWavFile(path, wavPath); err != nil {
		return err
	}

	changed, err := normalizeWavFile(wavPath)
	if err != nil {
		return err
	}

	if changed {
		if err := wavToOriginalFile(wavPath, path); err != nil {
			return err
		}
	}

	return nil
}

func toWavFile(path, wavPath string) error {
	return runFFMPEG(exec.Command(
		"ffmpeg",   // We let ffmpeg handle our decoding and conversion.
		"-y",       // Overwrite file if it exists.
		"-i", path, // Input file.
		"-bitexact",           // No extra headers in the wav.
		"-map_metadata", "-1", // Strip metadata (artist, track number, etc.).
		"-f", "wav", // Format as wav.
		"-c:a", "pcm_s16le", // Use int16 samples.
		"-ar", "44100", // Sample rate of 44100 Hz.
		"-ac", "2", // 2 channels.
		wavPath, // Write wav data to file.
	))
}

func runFFMPEG(cmd *exec.Cmd) error {
	_, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// exec.Commands return ExitErrors which have their Stderr written
			// to by ffmpeg. The err itself will usually just say "exit code 1"
			// while the real error context is given in stderr.
			return fmt.Errorf("%s\n", exitErr.Stderr)
		} else {
			return err
		}
	}
	return nil
}

func normalizeWavFile(wavPath string) (bool, error) {
	f, err := os.OpenFile(wavPath, os.O_RDWR, 0666)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// In a WAV file without any meta data the int16 sample stream start at byte
	// 44, after the RIFF header and the data header.
	// We read all int16 samples from the file, assuming that the whole rest of
	// the file contains only samples (i.e. that the data chunk is the last
	// chunk in the file).
	// We go over the file in two passes:
	// 1. Sum up the samples to build the average of all absolute sample 4
	//    amplitudes. This gives us the appropriate scale factor.
	// 2. Update all samples in the file with the scale factor.
	f.Seek(44, io.SeekStart)
	var (
		buf   [4096]byte
		sum   uint64
		count int
	)
	for {
		n, err := f.Read(buf[:])
		if n%2 == 1 {
			return false, errors.New("read odd number of bytes in int16 sample stream")
		}
		for i := 0; i < n; i += 2 {
			sample := int16(binary.LittleEndian.Uint16(buf[i:]))
			// We sum the absolute values of the samples.
			if sample < 0 {
				sum += uint64(-sample)
			} else {
				sum += uint64(sample)
			}
			count++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
	}

	// The scale is computed from the average amplitude of the WAV file.
	scale := scaleFactor * float64(count) / float64(sum)
	if 0.9 < scale && scale < 1.1 {
		// If the file is already normalized, do not do it again.
		return false, nil
	}

	// Now we skip back to the start and overwrite the file with the scaled
	// samples.
	_, err = f.Seek(44, io.SeekStart)
	if err != nil {
		return false, err
	}
	done := false
	for !done {
		n, err := f.Read(buf[:])
		done = err == io.EOF
		if !done && err != nil {
			return false, err
		}
		if n%2 == 1 {
			return false, errors.New("read odd number of bytes in int16 sample stream")
		}
		for i := 0; i < n; i += 2 {
			sample := int16(binary.LittleEndian.Uint16(buf[i:]))
			if sample < 0 {
				sample = int16(float64(sample)*scale - 0.5)
			} else {
				sample = int16(float64(sample)*scale + 0.5)
			}
			binary.LittleEndian.PutUint16(buf[i:], uint16(sample))
		}

		_, err = f.Seek(int64(-n), io.SeekCurrent)
		if err != nil {
			return false, err
		}
		_, err = f.Write(buf[:n])
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func wavToOriginalFile(wavPath, path string) error {
	return runFFMPEG(exec.Command(
		"ffmpeg",      // We let ffmpeg handle our decoding and conversion.
		"-y",          // Overwrite file if it exists.
		"-i", wavPath, // Input file.
		path, // Output file.
	))
}

package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Magic bytes to identify our custom file extension
var MagicNumber = []byte("EZF")

// FileMetadata holds the relative path and size of each file
type FileMetadata struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func main() {
	if len(os.Args) < 4 {
		fmt.Println("\nUsage:")
		fmt.Println("  Compress:   go run main.go -c [input_folder] [output_file.arcx]")
		fmt.Println("  Extract:    go run main.go -x [input_file.arcx] [output_folder]\n")
		os.Exit(1)
	}

	mode := os.Args[1]
	if mode == "-c" {
		compressFolder(os.Args[2], os.Args[3])
	} else if mode == "-x" {
		decompressArcx(os.Args[2], os.Args[3])
	} else {
		fmt.Println("Invalid flag. Use -c to compress or -x to extract.")
	}
}

func compressFolder(inputFolder, outputFile string) {
	fmt.Printf("Scanning folder: %s...\n", inputFolder)

	var manifest []FileMetadata
	var payload bytes.Buffer

	// 1. Walk through the directory and serialize file contents
	err := filepath.Walk(inputFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(inputFolder, path)
		if err != nil {
			return err
		}

		fileData, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("Skipping %s due to error: %v\n", relPath, err)
			return nil
		}

		manifest = append(manifest, FileMetadata{
			Path: relPath,
			Size: int64(len(fileData)),
		})
		payload.Write(fileData)

		return nil
	})

	if err != nil {
		fmt.Printf("Error scanning directory: %v\n", err)
		return
	}

	// Encode metadata layout to JSON
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		fmt.Printf("Error creating layout manifest: %v\n", err)
		return
	}

	// Construct uncompressed stream: [Manifest Length (4 bytes)] + [Manifest JSON] + [Raw Payload]
	var archiveStream bytes.Buffer
	manifestLen := uint32(len(manifestBytes))
	binary.Write(&archiveStream, binary.BigEndian, manifestLen)
	archiveStream.Write(manifestBytes)
	archiveStream.Write(payload.Bytes())

	fmt.Println("Compressing data stream via .arcx ultra engine...")

	// Create a zlib writer set to Best Compression (Level 9)
	var compressedData bytes.Buffer
	zlibWriter, err := zlib.NewWriterLevel(&compressedData, zlib.BestCompression)
	if err != nil {
		fmt.Printf("Error initializing compressor: %v\n", err)
		return
	}
	zlibWriter.Write(archiveStream.Bytes())
	zlibWriter.Close() // Flush and write zlib footer

	// 2. Write file to disk with custom header
	outFile, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return
	}
	defer outFile.Close()

	outFile.Write(MagicNumber)
	outFile.Write(compressedData.Bytes())

	fmt.Printf("Success! Custom archive created at: %s\n", outputFile)
}

func decompressArcx(inputFile, outputFolder string) {
	fmt.Printf("Reading custom archive: %s...\n", inputFile)

	// Ensure the root extraction destination folder exists safely up front
	err := os.MkdirAll(outputFolder, os.ModePerm)
	if err != nil {
		fmt.Printf("Error creating destination folder: %v\n", err)
		return
	}

	archiveBytes, err := os.ReadFile(inputFile)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	// Verify our custom magic header signature
	if len(archiveBytes) < 4 || !bytes.Equal(archiveBytes[:4], MagicNumber) {
		fmt.Println("Error: Invalid file format. Not a valid .arcx archive.")
		return
	}

	fmt.Println("Decompressing archive stream...")
	compressedPayload := archiveBytes[4:]

	b := bytes.NewReader(compressedPayload)
	zlibReader, err := zlib.NewReader(b)
	if err != nil {
		fmt.Printf("Decompression initialization failed: %v\n", err)
		return
	}
	defer zlibReader.Close()

	var decompressedStream bytes.Buffer
	_, err = io.Copy(&decompressedStream, zlibReader)
	if err != nil {
		fmt.Printf("Extraction failed. Stream corrupted: %v\n", err)
		return
	}

	rawStream := decompressedStream.Bytes()

	// Parse out metadata manifest length
	var manifestLen uint32
	binary.Read(bytes.NewReader(rawStream[:4]), binary.BigEndian, &manifestLen)

	manifestEnd := 4 + manifestLen
	manifestBytes := rawStream[4:manifestEnd]
	payloadBytes := rawStream[manifestEnd:]

	var manifest []FileMetadata
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		fmt.Printf("Failed to read structural metadata: %v\n", err)
		return
	}

	// 3. Rebuild folder tree and write raw files
	fmt.Printf("Extracting files to: %s...\n", outputFolder)
	var currentOffset int64 = 0

	for _, fileInfo := range manifest {
		destPath := filepath.Join(outputFolder, fileInfo.Path)

		// Ensure nested directory trees exist safely
		os.MkdirAll(filepath.Dir(destPath), os.ModePerm)

		// Prevent slice bounds out-of-range panics if payload is corrupted
		if currentOffset+fileInfo.Size > int64(len(payloadBytes)) {
			fmt.Printf("Error: Manifest sizes do not match payload constraints for %s\n", fileInfo.Path)
			return
		}

		extractedFileBytes := payloadBytes[currentOffset : currentOffset+fileInfo.Size]
		currentOffset += fileInfo.Size

		err := os.WriteFile(destPath, extractedFileBytes, os.ModePerm)
		if err != nil {
			fmt.Printf("Failed to write extracted file %s: %v\n", fileInfo.Path, err)
		}
	}

	fmt.Println("Extraction complete.")
}
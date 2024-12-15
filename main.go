package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/putdotio/go-putio"
	"golang.org/x/oauth2"
)

var (
	folderPath       string = os.Getenv("PUTIO_WATCH_FOLDER")
	putioToken       string = os.Getenv("PUTIO_TOKEN")
	downloadFolderID string = os.Getenv("PUTIO_DOWNLOAD_FOLDER_ID")
)

func connectToPutio() (*putio.Client, error) {
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: putioToken})
	oauthClient := oauth2.NewClient(oauth2.NoContext, tokenSource)

	client := putio.NewClient(oauthClient)

	return client, nil
}

func folderIDConvert() (int64, error) {
	folderID, err := strconv.ParseInt(downloadFolderID, 10, 32)
	if err != nil {
		str := fmt.Sprintf("strconv err: %v", err)
		err := errors.New(str)
		return 0, err
	}
	return folderID, nil
}

func uploadTorrentToPutio(filename string, filepath string, client *putio.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Second*5))
	defer cancel()

	// Convert FolderID from string to int to use with Files.Upload
	folderID, err := folderIDConvert()
	if err != nil {
		return err
	}

	// Using open since Upload need an *os.File variable
	log.Println("Reading...")
	file, err := os.Open(filename)
	if err != nil {
		str := fmt.Sprintf("Openfile err: %v", err)
		err := errors.New(str)
		return err
	}

	// Uploading file to Putio
	result, err := client.Files.Upload(ctx, file, filename, folderID)
	if err != nil {
		str := fmt.Sprintf("Upload to Putio err: %v", err)
		err := errors.New(str)
		return err
	}

	fmt.Printf("Transferred to putio:              %v at %v\n-------------------\n", filename, result.Transfer.CreatedAt)
	return nil
}

func transferMagnetToPutio(filename string, filepath string, client *putio.Client) error {
	// Creating a context with 5 second timout in case Transfer is too long
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(time.Second*5))
	defer cancel()

	// Convert FolderID from string to int to use with Files.Upload
	folderID, err := folderIDConvert()
	if err != nil {
		return err
	}

	// Reading the link inside the magnet file to give to Putio
	log.Println("Reading...")
	magnetData, err := ioutil.ReadFile(filename)
	if err != nil {
		str := fmt.Sprintf("Couldn't read file %v: %v", filename, err)
		err := errors.New(str)
		return err
	}
	log.Println("magnetData: ", string(magnetData))

	// Using Transfer to DL file via magnet file
	result, err := client.Transfers.Add(ctx, string(magnetData), folderID, "")
	if err != nil {
		str := fmt.Sprintf("Transfer to putio err: %v", err)
		err := errors.New(str)
		return err
	}

	fmt.Printf("Transferred to putio:              %v at %v\n-------------------\n", filename, result.CreatedAt)
	return nil
}

func checkFileType(filename string) (string, error) {
	// Checking what's at the end of the string
	isMagnet := strings.HasSuffix(filename, ".magnet")
	isTorrent := strings.HasSuffix(filename, ".torrent")

	if isMagnet {
		return "magnet", nil
	} else if isTorrent {
		return "torrent", nil
	} else {
		str := fmt.Sprintf("File isn't a torrent or magnet file: %v", filename)
		err := errors.New(str)
		return "", err
	}
}

func prepareFile(event fsnotify.Event, client *putio.Client) {
	time.Sleep(100 * time.Millisecond) // warte kurz, bis das Schreiben abgeschlossen ist

	var filepath string
	var err error
	var fileType string

	filename := event.Name

	torrentOrMagnet, err := checkFileType(filename)
	if err != nil {
		log.Println(err)
		return
	} else {
		fileType = torrentOrMagnet
	}

	fmt.Printf("Neue Datei erkannt: %v\n", filename)

	if fileType == "torrent" {
		err = uploadTorrentToPutio(filename, filepath, client)
		if err != nil {
			log.Println("Upload-Fehler:", err)
		} else {
			// Lösche die Datei nach erfolgreichem Upload
			os.Remove(filename)
		}
	} else if fileType == "magnet" {
		err = transferMagnetToPutio(filename, filepath, client)
		if err != nil {
			log.Println("Transfer-Fehler:", err)
		} else {
			// Lösche die Datei nach erfolgreichem Transfer
			os.Remove(filename)
		}
	}
}

func watchFolder(client *putio.Client) {
	// https://pkg.go.dev/github.com/fsnotify/fsnotify
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				log.Println("event:", event) // Flip on for verbose logging
				if event.Has(fsnotify.Create) {
					go prepareFile(event, client) // run in separate thread
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Fatalln(err)
			}
		}
	}()

	// Watch this folder for changes.
	if err := w.Add(folderPath); err != nil {
		log.Fatalln(err)
	}
	log.Println("Watching", folderPath)

	<-make(chan struct{})
}

func checkEnvVariables() error {
	var envToSet string

	if folderPath == "" {
		envToSet = "PUTIO_WATCH_FOLDER is not set / "
	}
	if downloadFolderID == "" {
		envToSet = envToSet + "PUTIO_DOWNLOAD_FOLDER_ID is not set / "
	}
	if putioToken == "" {
		envToSet = envToSet + "PUTIO_TOKEN is not set / "
	}
	if envToSet != "" {
		return errors.New(envToSet)
	}
	return nil
}

func main() {
	log.Println("Krantor Started")

	client, err := connectToPutio()
	if err != nil {
		log.Fatalln("connection to Putio err: ", err)
	}

	// Prüfe, ob alle nötigen ENV-Variablen gesetzt sind
	err = checkEnvVariables()
	if err != nil {
		log.Fatal(err)
	}

	// Vor dem Start des Watchers alle vorhandenen Torrent- oder Magnet-Dateien hochladen
	files, err := ioutil.ReadDir(folderPath)
	if err != nil {
		log.Fatalf("Fehler beim Lesen des Verzeichnisses %v: %v\n", folderPath, err)
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		filename := folderPath + "/" + f.Name()

		fileType, err := checkFileType(filename)
		if err != nil {
			// Ist keine Torrent/Magnet-Datei, ignoriere
			continue
		}

		if fileType == "torrent" {
			err = uploadTorrentToPutio(filename, "", client)
			if err != nil {
				log.Println("Upload-Fehler:", err)
			} else {
				os.Remove(filename)
			}
		} else if fileType == "magnet" {
			err = transferMagnetToPutio(filename, "", client)
			if err != nil {
				log.Println("Transfer-Fehler:", err)
			} else {
				os.Remove(filename)
			}
		}
	}

	// Starte den Watcher
	watchFolder(client)
}

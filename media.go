package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/joho/godotenv"
)

func main() {
	http.HandleFunc("/screenshot", handleNewScreenshot)
	http.HandleFunc("/attachment", handleNewAttachment)
	http.HandleFunc("/", handleIndex)
	fmt.Println(os.Getenv("APP_ENV"))

	if os.Getenv("APP_ENV") == "development" {
		// Load .env file
		err := godotenv.Load()
		if err != nil {
			log.Fatal("Error loading .env file")
		}
	}
	log.Fatal(http.ListenAndServe(":8000", nil))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello World")
}

func handleNewScreenshot(w http.ResponseWriter, r *http.Request) {

	type ScreenshotRequest struct {
		Id  string `json:"id"`
		Url string `json:"url"`
	}
	var screenshotRequest ScreenshotRequest

	err := json.NewDecoder(r.Body).Decode(&screenshotRequest)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	updateAirtableListingRecord(
		createAirtableMediaRecord(
			uploadToS3(
				downloadFiles(
					generateScreenshotUrl(screenshotRequest.Url), "screenshots")), screenshotRequest.Id),
		screenshotRequest.Id)
}

func handleNewAttachment(w http.ResponseWriter, r *http.Request) {
	type AttachmentRequest struct {
		Id          string `json:"id"`
		DownloadUrl string `json:"downloadUrl"`
	}
	var attachmentRequest AttachmentRequest

	err := json.NewDecoder(r.Body).Decode(&attachmentRequest)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	updateAirtableListingRecord(createAirtableMediaRecord(uploadToS3(downloadFiles(attachmentRequest.DownloadUrl, "screenshots")), attachmentRequest.Id), attachmentRequest.Id)
}

func generateScreenshotUrl(websiteUrl string) string {
	API_URL := os.Getenv("TECHULUS_API_URL")
	API_KEY := os.Getenv("TECHULUS_API_KEY")
	SECRET := os.Getenv("TECHULUS_SECRET")

	params := fmt.Sprintf("url=%s&delay=5", websiteUrl)
	hash := fmt.Sprintf("%x", md5.Sum([]byte(SECRET+params)))
	result_img_url := fmt.Sprintf("%s%s/%s/image?%s", API_URL, API_KEY, hash, params)

	return result_img_url
}

func downloadFiles(fileUrl string, directory string) []string {
	os.Mkdir(directory, 0755)
	// fileUrl could be a string containing multiple urls,
	// so we need to extract each and download them individually
	urlList := strings.Split(fileUrl, ",")
	downloadedFiles := make([]string, 0)

	// use goroutines and channels to download files concurrently
	filesChan := make(chan string)
	var wg sync.WaitGroup
	wg.Add(len(urlList))

	for index, url := range urlList {
		go func(index int, url string) {
			defer wg.Done()
			response, e := http.Get(url)
			if e != nil {
				log.Fatal(e)
			}
			defer response.Body.Close()
			file, err := os.CreateTemp(directory, "*.jpg")
			if err != nil {
				log.Fatal(err)
			}
			_, err = io.Copy(file, response.Body)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("Downloaded %s\n", file.Name())
			filesChan <- file.Name()
		}(index, strings.TrimSpace(url))
	}

	go func() {
		wg.Wait()
		close(filesChan)
	}()

	for file := range filesChan {
		downloadedFiles = append(downloadedFiles, file)
	}

	return downloadedFiles
}

// S3PutObjectAPI defines the interface for the PutObject function.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context,
		params *s3.PutObjectInput,
		optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// PutFile uploads a file to an Amazon Simple Storage Service (Amazon S3) bucket
// Inputs:
//     c is the context of the method call, which includes the AWS Region
//     api is the interface that defines the method call
//     input defines the input arguments to the service call.
// Output:
//     If success, a PutObjectOutput object containing the result of the service call and nil
//     Otherwise, nil and an error from the call to PutObject
func PutFile(c context.Context, api S3PutObjectAPI, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	return api.PutObject(c, input)
}

func uploadToS3(filenames []string) []string {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatalf("Failed to load S3 configuration, %v", err)
	}

	bucket := os.Getenv("AWS_S3_BUCKET")
	client := s3.NewFromConfig(cfg)

	uploadedURLs := make([]string, 0)
	urlsChan := make(chan string)

	var wg sync.WaitGroup
	wg.Add(len(filenames))

	for index, filename := range filenames {
		go func(index int, filename string) {
			defer wg.Done()
			stat, err := os.Stat(filename)
			if err != nil {
				fmt.Printf("Failed to get file size, %v", err)
			}
			filesize := stat.Size()

			fmt.Printf("The file is %d bytes long\n", filesize)

			file, err := os.Open(filename)

			if err != nil {
				panic("Couldn't open local file")
			}

			input := &s3.PutObjectInput{
				Bucket:        &bucket,
				Key:           &filename,
				Body:          file,
				ContentLength: filesize,
			}

			_, err = PutFile(context.TODO(), client, input)
			if err != nil {
				log.Fatalf(err.Error())
			}

			url := fmt.Sprintf("https://s3.%s.amazonaws.com/%s/%s", os.Getenv("AWS_REGION"), bucket, filename)

			defer os.Remove(file.Name()) // clean up

			fmt.Printf("File %s uploaded to S3 bucket %s\n with URL %s\n", filename, bucket, url)

			urlsChan <- url
		}(index, filename)
	}

	go func() {
		wg.Wait()
		close(urlsChan)
	}()

	for url := range urlsChan {
		uploadedURLs = append(uploadedURLs, url)
	}

	return uploadedURLs
}

func createAirtableMediaRecord(s3URLs []string, listingRecordId string) []string {

	// Request Types

	type AirtableAttachmentRequest struct {
		URL string `json:"url"`
	}

	type AirtableMediaRecordRequest struct {
		File     []AirtableAttachmentRequest `json:"File"`
		Link     string                      `json:"Link"`
		Listings []string                    `json:"Listings"`
	}

	type AirtableCreateMediaRequest struct {
		Fields AirtableMediaRecordRequest `json:"fields"`
	}

	type AirtableCreateMediaRecordRequest struct {
		Records []AirtableCreateMediaRequest `json:"records"`
	}

	createdRecords := make([]string, 0)
	createdRecordsChan := make(chan string)

	var wg sync.WaitGroup
	wg.Add(len(s3URLs))

	client := &http.Client{}

	for index, s3URL := range s3URLs {
		go func(index int, s3URL string) {
			defer wg.Done()
			createRequest := AirtableCreateMediaRecordRequest{
				Records: []AirtableCreateMediaRequest{
					{
						Fields: AirtableMediaRecordRequest{
							File: []AirtableAttachmentRequest{
								{
									URL: s3URL,
								},
							},
							Link:     s3URL,
							Listings: []string{listingRecordId},
						},
					},
				},
			}

			path := fmt.Sprintf("%s/%s/%s", os.Getenv("AIRTABLE_API_URL"), os.Getenv("AIRTABLE_BASE"), "Media")
			mediaRecordObj, requestParseError := json.Marshal(createRequest)

			if requestParseError != nil {
				log.Fatalf(requestParseError.Error())
			}
			request, requestError := http.NewRequest("POST", path, bytes.NewBuffer(mediaRecordObj))

			if requestError != nil {
				log.Fatalf(requestError.Error())
			}

			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("AIRTABLE_TOKEN")))

			response, responseError := client.Do(request)
			if responseError != nil {
				log.Fatalf(responseError.Error())
			}

			// Response Types

			type AirtableAttachmentResponse struct {
				URL      string `json:"url"`
				Id       string `json:"id"`
				FileName string `json:"filename"`
			}

			type AirtableMediaRecordResponse struct {
				Id       int                          `json:"Id"`
				Listings []string                     `json:"Listings"`
				Link     string                       `json:"Link"`
				File     []AirtableAttachmentResponse `json:"File"`
			}

			type AirtableCreateMediaResponse struct {
				Fields      AirtableMediaRecordResponse `json:"fields"`
				Id          string                      `json:"id"`
				CreatedTime string                      `json:"createdTime"`
			}

			type AirtableCreateMediaRecordResponse struct {
				Records []AirtableCreateMediaResponse `json:"records"`
			}

			var airtableCreateMediaRecordResponse AirtableCreateMediaRecordResponse

			responseParseError := json.NewDecoder(response.Body).Decode(&airtableCreateMediaRecordResponse)

			if responseParseError != nil {
				log.Fatalf(responseParseError.Error())
			}

			defer response.Body.Close()

			id := airtableCreateMediaRecordResponse.Records[0].Id

			fmt.Printf("Created Airtable record with id: %s", id)

			createdRecordsChan <- id
		}(index, s3URL)
	}

	go func() {
		wg.Wait()
		close(createdRecordsChan)
	}()

	for id := range createdRecordsChan {
		createdRecords = append(createdRecords, id)
	}

	return createdRecords
}

func updateAirtableListingRecord(mediaRecords []string, recordId string) {
	var wg sync.WaitGroup
	wg.Add(len(mediaRecords))

	client := &http.Client{}

	for index, mediaRecordId := range mediaRecords {
		go func(index int, mediaRecordId string) {
			defer wg.Done()
			// Request Types
			type AirtableListingRecordRequest struct {
				Images []string `json:"Images"`
			}

			type AirtableUpdateListingRequest struct {
				Id     string                       `json:"id"`
				Fields AirtableListingRecordRequest `json:"fields"`
			}

			type AirtableUpdateListingRecordRequest struct {
				Records []AirtableUpdateListingRequest `json:"records"`
			}

			updateRequest := AirtableUpdateListingRecordRequest{
				Records: []AirtableUpdateListingRequest{
					{
						Id: recordId,
						Fields: AirtableListingRecordRequest{
							Images: []string{mediaRecordId},
						},
					},
				},
			}

			path := fmt.Sprintf("%s/%s/%s", os.Getenv("AIRTABLE_API_URL"), os.Getenv("AIRTABLE_BASE"), "Listings")
			listingUpdateObj, requestParseError := json.Marshal(updateRequest)

			if requestParseError != nil {
				log.Fatalf(requestParseError.Error())
			}
			request, requestError := http.NewRequest("PATCH", path, bytes.NewBuffer(listingUpdateObj))

			if requestError != nil {
				log.Fatalf(requestError.Error())
			}

			request.Header.Set("Content-Type", "application/json; charset=UTF-8")
			request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("AIRTABLE_API_KEY")))

			response, err := client.Do(request)

			if err != nil {
				log.Fatalf(err.Error())
			}
			defer response.Body.Close()
		}(index, mediaRecordId)
	}

	wg.Wait()

}

// func updateAirtableMediaRecord(recordId string, s3Url string) {
//     // Request Types

//     type AirtableMediaRecordRequest struct {
//         Link string `json:"Link"`
//     }

//     type AirtableUpdateMediaRequest struct {
//         Id string `json:"id"`
//         Fields AirtableMediaRecordRequest `json:"fields"`
//     }

//     type AirtableUpdateMediaRecordRequest struct {
//         Records []AirtableUpdateMediaRequest `json:"records"`
//     }

//     updateRequest := AirtableUpdateMediaRecordRequest{
// 		Records: []AirtableUpdateMediaRequest{
// 			{
//                 Id: recordId,
// 				Fields: AirtableMediaRecordRequest{
// 					Link: s3Url,
// 					},
// 				},
// 			},
// 		}

//     path := fmt.Sprintf("%s/%s/%s", os.Getenv("AIRTABLE_API_URL"), os.Getenv("AIRTABLE_BASE"), "Media")
//     listingUpdateObj, requestParseError := json.Marshal(updateRequest)

//     if requestParseError != nil {
// 		log.Fatalf(requestParseError.Error())
// 	}
//     request, requestError := http.NewRequest("PATCH", path, bytes.NewBuffer(listingUpdateObj))

// 	if requestError != nil {
// 		log.Fatalf(requestError.Error())
// 	}

// 	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
//     request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("AIRTABLE_API_KEY")))

// 	client := &http.Client{}
// 	response, err := client.Do(request)

//     fmt.Printf(response.Status)
// 	if err != nil {
// 		log.Fatalf(err.Error())
// 	}
//     defer response.Body.Close()
// }

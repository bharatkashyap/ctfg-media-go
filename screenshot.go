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

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"	
)


func main() {	         
	http.HandleFunc("/screenshot", handleNewScreenshot)    
  http.HandleFunc("/", handleIndex)      
  log.Fatal(http.ListenAndServe(":8000", nil))	
}

func handleIndex(w http.ResponseWriter, r *http.Request)  {
    fmt.Fprintf(w, "Hello World")
}



func handleNewScreenshot(w http.ResponseWriter, r *http.Request)  {
	   
    type ScreenshotRequest struct {
        Id string `json:"id"`
        Url string  `json:"url"`
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
                downloadScreenshot(
                    generateScreenshotUrl(screenshotRequest.Url)))), 
    screenshotRequest.Id)
               
}

func generateScreenshotUrl(websiteUrl string) string {
    API_URL := os.Getenv("TECHULUS_API_URL")
    API_KEY := os.Getenv("TECHULUS_API_KEY")
    SECRET := os.Getenv("TECHULUS_SECRET")

    params := fmt.Sprintf("url=%s&delay=5", websiteUrl)
    hash := fmt.Sprintf("%x", md5.Sum([]byte(SECRET + params)))
    result_img_url := fmt.Sprintf("%s%s/%s/image?%s", API_URL, API_KEY, hash, params) 
    
    return result_img_url  
}

func downloadScreenshot(screenshotUrl string) string {
    response, e := http.Get(screenshotUrl)
    if e != nil {
        log.Fatal(e)
    }
    defer response.Body.Close()

    os.Mkdir("screenshots", 0755)

    file, err := os.CreateTemp("screenshots", "*.jpg")
    if err != nil {
        log.Fatal(err)
    }    
    _, err = io.Copy(file, response.Body)
    if err != nil {
        log.Fatal(err)
    }
    return file.Name()
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


func uploadToS3(filename string) string {
    cfg, err := config.LoadDefaultConfig(context.TODO())
    if err != nil {
        log.Fatalf("Failed to load S3 configuration, %v", err)
    }

    stat, err := os.Stat(filename)    
    if err != nil {
       fmt.Printf("Failed to get file size, %v", err)
    }
    filesize := stat.Size()

    fmt.Printf("The file is %d bytes long\n", filesize)

    bucket := os.Getenv("AWS_S3_BUCKET")	

    client := s3.NewFromConfig(cfg)

    file, err := os.Open(filename)

	if err != nil {
		panic("Couldn't open local file")
	}

    input := &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &filename,
		Body:   file,      
        ContentLength: filesize,  
	}


	_, err = PutFile(context.TODO(), client, input)
	if err != nil {
		log.Fatalf(err.Error())
	}

    url:= fmt.Sprintf("https://s3.%s.amazonaws.com/%s/%s", os.Getenv("AWS_REGION"), bucket, filename)

    defer os.Remove(file.Name()) // clean up

    fmt.Printf("File %s uploaded to S3 bucket %s\n with URL %s\n", filename, bucket, url)

    return url
}

func createAirtableMediaRecord(s3URL string) string {

    // Request Types

    type AirtableAttachmentRequest struct {
        URL string `json:"url"`
    }
    
    type AirtableMediaRecordRequest struct {
        Attachments []AirtableAttachmentRequest `json:"Attachments"`
        URL string `json:"URL"`
    }

    type AirtableCreateMediaRequest struct {               
        Fields AirtableMediaRecordRequest `json:"fields"`
    }

    type AirtableCreateMediaRecordRequest struct {
        Records []AirtableCreateMediaRequest `json:"records"`
    }

    
    createRequest := AirtableCreateMediaRecordRequest{
		Records: []AirtableCreateMediaRequest{
			{				
				Fields: AirtableMediaRecordRequest{
					Attachments: []AirtableAttachmentRequest{
						{
							URL: s3URL,
						},
					},	
                    URL: s3URL,
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
    request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("AIRTABLE_API_KEY")))

	client := &http.Client{}
	response, responseError := client.Do(request)
	if responseError != nil {
		log.Fatalf(responseError.Error())
	}    

     // Response Types

     type AirtableAttachmentResponse struct {
        URL string `json:"url"`
        Id string `json:"id"`
        FileName string `json:"filename"`
    }

    type AirtableMediaRecordResponse struct {
        Id int `json:"Id"`
        Attachments []AirtableAttachmentResponse `json:"Attachments"`
    }

    type AirtableCreateMediaResponse struct {               
        Fields AirtableMediaRecordResponse `json:"fields"`
        Id string `json:"id"`
        CreatedTime string `json:"createdTime"`
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
     return id
}

func updateAirtableListingRecord(mediaRecordId string, recordId string)  {    
   // Request Types
    type AirtableListingRecordRequest struct {
        Image []string `json:"Image"`
    }

    type AirtableUpdateListingRequest struct {               
        Id string `json:"id"`
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
					Image: []string{mediaRecordId},
					},					
				},
			},
		}	

    path := fmt.Sprintf("%s/%s/%s", os.Getenv("AIRTABLE_API_URL"), os.Getenv("AIRTABLE_BASE"), "Listing")     
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

	client := &http.Client{}
	response, err := client.Do(request)

    fmt.Printf(response.Status)
	if err != nil {
		log.Fatalf(err.Error())
	}	
    defer response.Body.Close()    
}

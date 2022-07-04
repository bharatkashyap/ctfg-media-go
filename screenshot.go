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
	"github.com/joho/godotenv"
)


func main() {	
    err := godotenv.Load()
    if err != nil {
        log.Fatal("Error loading .env file")
    }       
	http.HandleFunc("/screenshot", handleNewScreenshot)      
    log.Fatal(http.ListenAndServe(":6000", nil))	
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

    updateAirtableListingRecord(createAirtableMediaRecord(uploadToS3(downloadScreenshot(generateScreenshotUrl(screenshotRequest.Url)))), screenshotRequest.Id)
               
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

func downloadScreenshot(screenshotUrl string) *os.File {
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
    return file

}

// S3PutObjectAPI defines the interface for the PutObject function.
// We use this interface to test the function using a mocked service.
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


func uploadToS3(file *os.File) string {
    cfg, err := config.LoadDefaultConfig(context.TODO())
    if err != nil {
        log.Fatalf("Failed to load S3 configuration, %v", err)
    }


    bucket := os.Getenv("AWS_S3_BUCKET")
	filename := file.Name()

    client := s3.NewFromConfig(cfg)

    input := &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &filename,
		Body:   file,        
	}

    fi, err := file.Stat()
    if err != nil {
        fmt.Printf("Failed to get file size, %v", err)
    }

    fmt.Printf("The file is %d bytes long", fi.Size())

	_, err = PutFile(context.TODO(), client, input)
	if err != nil {
		log.Fatalf(err.Error())
	}

    url:= fmt.Sprintf("https://s3.%s.amazonaws.com/%s/%s", os.Getenv("AWS_REGION"), bucket, filename)

    defer os.Remove(file.Name()) // clean up

    return fmt.Sprintf("File %s uploaded to S3 bucket %s\n with URL %s", filename, bucket, url)

}

func createAirtableMediaRecord(s3URL string) string {

    type AirtableAttachment struct {
        URL string `json:"url"`
    }
    
    type AirtableMediaRecord struct {
        Attachments []AirtableAttachment `json:"Attachments"`
    }
    
    type AirtableCreateMediaRecord struct {       
        Id string `json:"id"` 
        Fields AirtableMediaRecord `json:"fields"`
    }
    
    type AirtableCreateMediaRecordRequest struct {
        Records []AirtableCreateMediaRecord `json:"records"`
    }

    mediaRecord := AirtableCreateMediaRecordRequest{
		Records: []AirtableCreateMediaRecord{
			{				
				Fields: AirtableMediaRecord{
					Attachments: []AirtableAttachment{
						{
							URL: s3URL,
						},
					},					
				},
			},
		},
	}
        

    path := fmt.Sprintf("%s/%s/%s", os.Getenv("AIRTABLE_API_URL"), os.Getenv("AIRTABLE_BASE"), "Media")
    mediaRecordObj, requestParseError := json.Marshal(mediaRecord)   


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

    type AirtableCreateMediaRecordResponse struct {
        Records []AirtableCreateMediaRecord `json:"records"`
    }
	var airtableCreateMediaRecordResponse AirtableCreateMediaRecordResponse
    
    responseParseError := json.NewDecoder(response.Body).Decode(&airtableCreateMediaRecordResponse)    

    if responseParseError != nil {            
        log.Fatalf(responseParseError.Error())        
    }
    
    defer response.Body.Close()
        
    return airtableCreateMediaRecordResponse.Records[0].Id
}

func updateAirtableListingRecord(mediaRecordId string, recordId string)  {
    

    var listingRecord = []byte(fmt.Sprintf(`{
        "records": [         
          {
            "id": "%s",
            "fields": {              
              "Image": [
                "%s"
              ]             
            }
          }
        ]
      }'`, recordId, mediaRecordId))

    path := fmt.Sprintf("%s/%s/%s", os.Getenv("AIRTABLE_API_URL"), os.Getenv("AIRTABLE_BASE"), "Listing")     
    request, requestError := http.NewRequest("PATCH", path, bytes.NewBuffer(listingRecord))
    if requestError != nil {
		log.Fatalf(requestError.Error())
	}

	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
    request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", os.Getenv("AIRTABLE_API_KEY")))

	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		log.Fatalf(err.Error())
	}
	
    defer response.Body.Close()
    
}

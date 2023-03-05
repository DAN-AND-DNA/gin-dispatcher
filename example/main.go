package main

import (
	"context"
	ginDispatcher "github.com/dan-and-dna/gin-dispatcher"
	"github.com/gin-gonic/gin"
	"log"
)

type EchoRequest struct {
	Message string `json:"message"`
	Id      int    `json:"-"`
}

type EchoResponse struct {
	Message string `json:"message"`
	Name    string `json:"name"`
	Age     int    `json:"-"`
}

func main() {
	r := gin.Default()

	messages := ginDispatcher.NewMessages()
	/*
		messages.MessageId = func(c *gin.Context) string {
			return c.PostForm("id")
		}
		messages.Payload = func(c *gin.Context) string {
			return c.PostForm("msg")
		}
		messages.HandleError = func(c *gin.Context, err error) {
			if _, ok := err.(*validator.InvalidValidationError); ok {
				fmt.Println(err)
			}

			for _, err := range err.(validator.ValidationErrors) {
				fmt.Println(err.StructNamespace(), err.Field())
			}

			c.JSON(http.StatusOK, gin.H{"code": -1, "error": err.Error()})
		}
	*/
	messages.Register("30001", func(c context.Context, req *EchoRequest, resp *EchoResponse) error {
		log.Println(req)
		log.Println(resp)

		req.Id = 37
		resp.Message = req.Message
		resp.Age = 111
		resp.Name = "dddd"
		return nil
	})

	// r.Use(ginDispatcher.GinDispatcher(messages))
	// or
	r.GET("/dmm", ginDispatcher.GinDispatcher(messages))

	r.Run()
}

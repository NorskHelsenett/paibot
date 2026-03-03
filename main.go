package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	verbose := flag.Bool("verbose", false, "Enable verbose/debug logging")
	flag.Parse()
	if !*verbose {
		// Also check VERBOSE_LOGS env var
		env := strings.ToLower(os.Getenv("VERBOSE_LOGS"))
		if env == "1" || env == "true" || env == "yes" {
			*verbose = true
		}
	}

	appToken := os.Getenv("SLACK_APP_TOKEN")
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	openAIBaseURL := os.Getenv("OPENAI_BASE_URL")
	openAIKey := os.Getenv("OPENAI_API_KEY")
	openAIModel := os.Getenv("OPENAI_MODEL")

	if appToken == "" || botToken == "" {
		log.Fatal("SLACK_APP_TOKEN and SLACK_BOT_TOKEN must be set")
	}
	if openAIBaseURL == "" || openAIKey == "" || openAIModel == "" {
		log.Fatal("OPENAI_BASE_URL, OPENAI_API_KEY, and OPENAI_MODEL must be set")
	}

	ai := NewAIClient(openAIBaseURL, openAIKey, openAIModel)

	clientOpts := []slack.Option{
		slack.OptionAppLevelToken(appToken),
	}
	if *verbose {
		clientOpts = append(clientOpts,
			slack.OptionDebug(true),
			slack.OptionLog(log.New(os.Stdout, "slack: ", log.LstdFlags|log.Lshortfile)),
		)
	}
	client := slack.New(botToken, clientOpts...)

	// Verify bot token and print identity for diagnostics
	authResp, err := client.AuthTest()
	if err != nil {
		log.Fatalf("Auth test failed (is SLACK_BOT_TOKEN valid?): %v", err)
	}
	log.Printf("Bot authenticated: user=%s (ID=%s) team=%s (ID=%s)",
		authResp.User, authResp.UserID, authResp.Team, authResp.TeamID)

	socketOpts := []socketmode.Option{}
	if *verbose {
		socketOpts = append(socketOpts,
			socketmode.OptionDebug(true),
			socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.LstdFlags|log.Lshortfile)),
		)
	}
	socketClient := socketmode.New(client, socketOpts...)

	go handleEvents(client, ai, authResp.UserID, socketClient)

	if err := socketClient.Run(); err != nil {
		log.Fatalf("Socket mode error: %v", err)
	}
}

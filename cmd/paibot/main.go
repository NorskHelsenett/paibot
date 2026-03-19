package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/jonasbg/paibot/internal/ai"
	"github.com/jonasbg/paibot/internal/bot"
	"github.com/jonasbg/paibot/internal/config"
	"github.com/jonasbg/paibot/internal/logutil"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.BoolVar(&logutil.Verbose, "verbose", false, "Enable verbose/debug logging")
	flag.Parse()
	if !logutil.Verbose {
		env := strings.ToLower(os.Getenv("VERBOSE_LOGS"))
		if env == "1" || env == "true" || env == "yes" {
			logutil.Verbose = true
		}
	}

	botCfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	log.Printf("Config loaded: model=%s temperature=%.1f", botCfg.Model, botCfg.Temp)

	appToken := os.Getenv("SLACK_APP_TOKEN")
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	openAIBaseURL := os.Getenv("OPENAI_BASE_URL")
	openAIKey := os.Getenv("OPENAI_API_KEY")

	if appToken == "" || botToken == "" {
		log.Fatal("SLACK_APP_TOKEN and SLACK_BOT_TOKEN must be set")
	}
	if openAIBaseURL == "" || openAIKey == "" {
		log.Fatal("OPENAI_BASE_URL and OPENAI_API_KEY must be set")
	}

	aiClient := ai.NewClient(openAIBaseURL, openAIKey, botCfg)

	clientOpts := []slack.Option{
		slack.OptionAppLevelToken(appToken),
	}
	if logutil.Verbose {
		clientOpts = append(clientOpts,
			slack.OptionDebug(true),
			slack.OptionLog(log.New(os.Stdout, "slack: ", log.LstdFlags|log.Lshortfile)),
		)
	}
	client := slack.New(botToken, clientOpts...)

	authResp, err := client.AuthTest()
	if err != nil {
		log.Fatalf("Auth test failed (is SLACK_BOT_TOKEN valid?): %v", err)
	}
	log.Printf("Bot authenticated: user=%s (ID=%s) team=%s (ID=%s)",
		authResp.User, authResp.UserID, authResp.Team, authResp.TeamID)

	socketOpts := []socketmode.Option{}
	if logutil.Verbose {
		socketOpts = append(socketOpts,
			socketmode.OptionDebug(true),
			socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.LstdFlags|log.Lshortfile)),
		)
	}
	socketClient := socketmode.New(client, socketOpts...)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	var wg sync.WaitGroup

	// Start event handler in a goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		bot.HandleEvents(client, aiClient, botToken, socketClient)
	}()

	// Start socket client in a goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := socketClient.Run(); err != nil {
			log.Fatalf("Socket mode error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received, marking in-flight messages as error...")
	bot.MarkInFlightAsError(client)

	// Close socket client
	socketClient.Close()

	// Wait for all goroutines to complete
	wg.Wait()
	log.Println("Graceful shutdown completed")
}

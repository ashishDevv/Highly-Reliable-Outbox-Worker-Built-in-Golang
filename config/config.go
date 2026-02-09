package config

// import (
// 	"log"
// 	"os"
// 	"strconv"

// 	"github.com/joho/godotenv"
// )

// type Config struct {
// 	DB_URL string      // Database connection URL
// 	// Port   int         // Application port
// 	Env    string      // Environment (development, production, etc.)
// 	RabbitMQConfig *RabbitMQConfig
// 	RunnerConfig   *RunnerConfig
// }

// func LoadConfig() *Config {    // Contorstrucstor function
// 	err := godotenv.Load()
// 	if err != nil {
// 		log.Fatalf("Error in Loading Env Variables: %v", err)
// 	}

// 	dbUrl := os.Getenv("DB_URL")
// 	env := os.Getenv("ENV")
// 	// port := getEnvAsInt("PORT", 3000)
// 	brokerLink := os.Getenv("RABBITMQ_BROKER_LINK")
// 	exchangeName := os.Getenv("RABBITMQ_EXCHANGE_NAME")
// 	exchangeType := os.Getenv("RABBITMQ_EXCHANGE_TYPE")
// 	queueName := os.Getenv("RABBITMQ_QUEUE_NAME")
// 	routingKey := os.Getenv("RABBITMQ_ROUTING_KEY")
// 	intervalTimeSeconds := getEnvAsInt("RUNNER_INTERVAL_TIME_SECONDS", 5)
// 	batchSize := getEnvAsInt("RUNNER_BATCH_SIZE", 50)

// 	// we have to check that all required env load , if not then log fatal


// 	return &Config{
// 		DB_URL: dbUrl,
// 		Env: env,
// 		RabbitMQConfig: &RabbitMQConfig{
// 			BrokerLink: brokerLink,
// 			ExchangeName: exchangeName,
// 			ExchangeType: exchangeType,
// 			QueueName: queueName,
// 			RoutingKey: routingKey,
// 		},
// 		RunnerConfig: &RunnerConfig{
// 			IntervalTimeSeconds: intervalTimeSeconds,
// 			BatchSize: batchSize,
// 		},
// 	}
// }


// func getEnvAsInt(key string, defaultVal int) int {
// 	val := os.Getenv(key)
// 	if val == "" {
// 		return defaultVal
// 	}
// 	num, err := strconv.Atoi(val)
// 	if err != nil {
// 		log.Printf("Invalid value of %s, using default %v", key, defaultVal)
// 		return defaultVal
// 	}
// 	return num
// }
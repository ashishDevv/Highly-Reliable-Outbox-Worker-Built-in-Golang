package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
)

func LoadConfig(path string) *Config {
	v := viper.New()
	v.SetConfigFile(path)   // path to your YAML file
	v.SetConfigType("yaml") // optional if file has .yaml extension

	// Set defaults
	v.SetDefault("env", "development")
	v.SetDefault("runner.count", 5)
	v.SetDefault("runner.batch_size", 10)
	v.SetDefault("runner.interval_time_sec", 5)
	v.SetDefault("service_name", "cosmic-user-service")

	// Env overrides
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		log.Fatalf("Error unmarshaling config: %v", err)
	}

	// Validation
	if cfg.DBURL == "" {
		log.Fatal("DB_URL is required")
	}
	
	if cfg.RabbitMQ == nil {
		log.Fatal("RabbitMQ configrations are required")
	}

	return &cfg
}



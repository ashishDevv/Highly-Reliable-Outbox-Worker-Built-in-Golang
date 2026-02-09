package config

type RabbitMQConfig struct {
	BrokerLink   string `mapstructure:"broker_link"`
	ExchangeName string `mapstructure:"exchange_name"`
	ExchangeType string `mapstructure:"exchange_type"`
	QueueName    string `mapstructure:"queue_name"`
	RoutingKey   string `mapstructure:"routing_key"`
}

type RunnerConfig struct {
	IntervalTimeSeconds int `mapstructure:"interval_time_sec"`
	BatchSize           int `mapstructure:"batch_size"`
	Count               int `mapstructure:"count"`
}

type Config struct {
	DBURL       string          `mapstructure:"db_url"`
	Env         string          `mapstructure:"env"`
	ServiceName string          `mapstructure:"service_name"`
	RedisAddr   string          `mapstructure:"redis_addr"`
	Runner      *RunnerConfig   `mapstructure:"runner"`
	RabbitMQ    *RabbitMQConfig `mapstructure:"rabbitmq"`
}

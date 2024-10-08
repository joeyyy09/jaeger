// Copyright (c) 2019 The Jaeger Authors.
// Copyright (c) 2017 Uber Technologies, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/gocql/gocql"
	"go.uber.org/zap"

	"github.com/jaegertracing/jaeger/pkg/cassandra"
	gocqlw "github.com/jaegertracing/jaeger/pkg/cassandra/gocql"
	"github.com/jaegertracing/jaeger/pkg/config/tlscfg"
)

// Configuration describes the configuration properties needed to connect to a Cassandra cluster
type Configuration struct {
	Servers              []string       `valid:"required,url" mapstructure:"servers"`
	Keyspace             string         `mapstructure:"keyspace"`
	LocalDC              string         `mapstructure:"local_dc"`
	ConnectionsPerHost   int            `mapstructure:"connections_per_host"`
	Timeout              time.Duration  `mapstructure:"-"`
	ConnectTimeout       time.Duration  `mapstructure:"connection_timeout"`
	ReconnectInterval    time.Duration  `mapstructure:"reconnect_interval"`
	SocketKeepAlive      time.Duration  `mapstructure:"socket_keep_alive"`
	MaxRetryAttempts     int            `mapstructure:"max_retry_attempts"`
	ProtoVersion         int            `mapstructure:"proto_version"`
	Consistency          string         `mapstructure:"consistency"`
	DisableCompression   bool           `mapstructure:"disable_compression"`
	Port                 int            `mapstructure:"port"`
	Authenticator        Authenticator  `mapstructure:",squash"`
	DisableAutoDiscovery bool           `mapstructure:"-"`
	TLS                  tlscfg.Options `mapstructure:"tls"`
}

func DefaultConfiguration() Configuration {
	return Configuration{
		Servers:            []string{"127.0.0.1"},
		Port:               9042,
		MaxRetryAttempts:   3,
		Keyspace:           "jaeger_v1_test",
		ProtoVersion:       4,
		ConnectionsPerHost: 2,
		ReconnectInterval:  60 * time.Second,
	}
}

// Authenticator holds the authentication properties needed to connect to a Cassandra cluster
type Authenticator struct {
	Basic BasicAuthenticator `yaml:"basic" mapstructure:",squash"`
	// TODO: add more auth types
}

// BasicAuthenticator holds the username and password for a password authenticator for a Cassandra cluster
type BasicAuthenticator struct {
	Username              string   `yaml:"username" mapstructure:"username"`
	Password              string   `yaml:"password" mapstructure:"password" json:"-"`
	AllowedAuthenticators []string `yaml:"allowed_authenticators" mapstructure:"allowed_authenticators"`
}

// ApplyDefaults copies settings from source unless its own value is non-zero.
func (c *Configuration) ApplyDefaults(source *Configuration) {
	if c.ConnectionsPerHost == 0 {
		c.ConnectionsPerHost = source.ConnectionsPerHost
	}
	if c.MaxRetryAttempts == 0 {
		c.MaxRetryAttempts = source.MaxRetryAttempts
	}
	if c.Timeout == 0 {
		c.Timeout = source.Timeout
	}
	if c.ReconnectInterval == 0 {
		c.ReconnectInterval = source.ReconnectInterval
	}
	if c.Port == 0 {
		c.Port = source.Port
	}
	if c.Keyspace == "" {
		c.Keyspace = source.Keyspace
	}
	if c.ProtoVersion == 0 {
		c.ProtoVersion = source.ProtoVersion
	}
	if c.SocketKeepAlive == 0 {
		c.SocketKeepAlive = source.SocketKeepAlive
	}
}

// SessionBuilder creates new cassandra.Session
type SessionBuilder interface {
	NewSession(logger *zap.Logger) (cassandra.Session, error)
}

// NewSession creates a new Cassandra session
func (c *Configuration) NewSession(logger *zap.Logger) (cassandra.Session, error) {
	cluster, err := c.NewCluster(logger)
	if err != nil {
		return nil, err
	}
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, err
	}
	return gocqlw.WrapCQLSession(session), nil
}

// NewCluster creates a new gocql cluster from the configuration
func (c *Configuration) NewCluster(logger *zap.Logger) (*gocql.ClusterConfig, error) {
	cluster := gocql.NewCluster(c.Servers...)
	cluster.Keyspace = c.Keyspace
	cluster.NumConns = c.ConnectionsPerHost
	cluster.Timeout = c.Timeout
	cluster.ConnectTimeout = c.ConnectTimeout
	cluster.ReconnectInterval = c.ReconnectInterval
	cluster.SocketKeepalive = c.SocketKeepAlive
	if c.ProtoVersion > 0 {
		cluster.ProtoVersion = c.ProtoVersion
	}
	if c.MaxRetryAttempts > 1 {
		cluster.RetryPolicy = &gocql.SimpleRetryPolicy{NumRetries: c.MaxRetryAttempts - 1}
	}
	if c.Port != 0 {
		cluster.Port = c.Port
	}

	if !c.DisableCompression {
		cluster.Compressor = gocql.SnappyCompressor{}
	}

	if c.Consistency == "" {
		cluster.Consistency = gocql.LocalOne
	} else {
		cluster.Consistency = gocql.ParseConsistency(c.Consistency)
	}

	fallbackHostSelectionPolicy := gocql.RoundRobinHostPolicy()
	if c.LocalDC != "" {
		fallbackHostSelectionPolicy = gocql.DCAwareRoundRobinPolicy(c.LocalDC)
	}
	cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(fallbackHostSelectionPolicy, gocql.ShuffleReplicas())

	if c.Authenticator.Basic.Username != "" && c.Authenticator.Basic.Password != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username:              c.Authenticator.Basic.Username,
			Password:              c.Authenticator.Basic.Password,
			AllowedAuthenticators: c.Authenticator.Basic.AllowedAuthenticators,
		}
	}
	tlsCfg, err := c.TLS.Config(logger)
	if err != nil {
		return nil, err
	}
	if c.TLS.Enabled {
		cluster.SslOpts = &gocql.SslOptions{
			Config: tlsCfg,
		}
	}
	// If tunneling connection to C*, disable cluster autodiscovery features.
	if c.DisableAutoDiscovery {
		cluster.DisableInitialHostLookup = true
		cluster.IgnorePeerAddr = true
	}
	return cluster, nil
}

func (c *Configuration) Close() error {
	return c.TLS.Close()
}

func (c *Configuration) String() string {
	return fmt.Sprintf("%+v", *c)
}

func (c *Configuration) Validate() error {
	_, err := govalidator.ValidateStruct(c)
	return err
}

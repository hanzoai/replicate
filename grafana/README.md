# Replicate Grafana Dashboard

This directory contains a Grafana dashboard for monitoring Replicate metrics.

## Prerequisites

1. Replicate configured with metrics endpoint enabled in `replicate.yml`:

   ```yaml
   addr: ":9090"
   ```

2. Prometheus configured to scrape Replicate metrics:

   ```yaml
   scrape_configs:
     - job_name: 'replicate'
       static_configs:
         - targets: ['localhost:9090']
   ```

3. Grafana with Prometheus data source configured

## Installation

1. Open Grafana and navigate to **Dashboards** → **Import**
2. Upload the `replicate-dashboard.json` file or paste its contents
3. Select your Prometheus data source
4. Click **Import**

## Metrics Included

The dashboard monitors the following key metrics:

- **Database & WAL Size**: Current size of the database and Write-Ahead Log
- **Total WAL Bytes Written**: Cumulative bytes written to shadow WAL
- **Sync Operations**: Rate of sync operations and any sync errors
- **Sync Duration**: Time spent syncing shadow WAL
- **Checkpoint Operations**: Rate of checkpoint operations by mode
- **Checkpoint Errors**: Any checkpoint errors that occur
- **Transaction ID**: Current transaction ID for each database
- **Replica Operations**: Operations performed by replica type (GET/PUT)
- **Replica Throughput**: Bytes transferred by replica operations

## Configuration

The dashboard uses template variables:

- `datasource`: Select your Prometheus data source
- `job`: Select the Prometheus job name (defaults to "replicate")

## Support

For issues or improvements to this dashboard, please open an issue at:
<https://github.com/benbjohnson/replicate/issues>

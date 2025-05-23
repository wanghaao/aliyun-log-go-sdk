package sls

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-kit/kit/log/level"
)

type TokenAutoUpdateClient struct {
	logClient              ClientInterface
	shutdown               <-chan struct{}
	closeFlag              bool
	tokenUpdateFunc        UpdateTokenFunction
	maxTryTimes            int
	waitIntervalMin        time.Duration
	waitIntervalMax        time.Duration
	updateTokenIntervalMin time.Duration
	nextExpire             time.Time

	lock               sync.Mutex
	lastFetch          time.Time
	lastRetryFailCount int
	lastRetryInterval  time.Duration
}

var errSTSFetchHighFrequency = errors.New("sts token fetch frequency is too high")

func (c *TokenAutoUpdateClient) flushSTSToken() {
	for {
		nowTime := time.Now()
		c.lock.Lock()
		sleepTime := c.nextExpire.Sub(nowTime)
		if sleepTime < time.Duration(time.Minute) {
			sleepTime = time.Duration(time.Second * 30)

		} else if sleepTime < time.Duration(time.Minute*10) {
			sleepTime = sleepTime / 10 * 7
		} else if sleepTime < time.Duration(time.Hour) {
			sleepTime = sleepTime / 10 * 6
		} else {
			sleepTime = sleepTime / 10 * 5
		}
		c.lock.Unlock()
		if IsDebugLevelMatched(1) {
			level.Info(Logger).Log("msg", "next fetch sleep interval : ", sleepTime.String())
		}
		trigger := time.After(sleepTime)
		select {
		case <-trigger:
			err := c.fetchSTSToken()
			if IsDebugLevelMatched(1) {
				level.Info(Logger).Log("msg", "fetch sts token done, error : ", err)
			}
		case <-c.shutdown:
			if IsDebugLevelMatched(1) {
				level.Info(Logger).Log("msg", "receive shutdown signal, exit flushSTSToken")
			}
			return
		}
		if c.closeFlag {
			if IsDebugLevelMatched(1) {
				level.Info(Logger).Log("msg", "close flag is true, exit flushSTSToken")
			}
			return
		}
	}

}

func (c *TokenAutoUpdateClient) fetchSTSToken() error {
	nowTime := time.Now()
	skip := false
	sleepTime := time.Duration(0)
	c.lock.Lock()
	if nowTime.Sub(c.lastFetch) < c.updateTokenIntervalMin {
		skip = true
	} else {
		c.lastFetch = nowTime
		if c.lastRetryFailCount == 0 {
			sleepTime = 0
		} else {
			c.lastRetryInterval *= 2
			if c.lastRetryInterval < c.waitIntervalMin {
				c.lastRetryInterval = c.waitIntervalMin
			}
			if c.lastRetryInterval >= c.waitIntervalMax {
				c.lastRetryInterval = c.waitIntervalMax
			}
			sleepTime = c.lastRetryInterval
		}
	}
	c.lock.Unlock()
	if skip {
		return errSTSFetchHighFrequency
	}
	if sleepTime > time.Duration(0) {
		time.Sleep(sleepTime)
	}

	accessKeyID, accessKeySecret, securityToken, expireTime, err := c.tokenUpdateFunc()
	if err == nil {
		c.lock.Lock()
		c.lastRetryFailCount = 0
		c.lastRetryInterval = time.Duration(0)
		c.nextExpire = expireTime
		c.lock.Unlock()
		c.logClient.ResetAccessKeyToken(accessKeyID, accessKeySecret, securityToken)
		if IsDebugLevelMatched(1) {
			level.Info(Logger).Log("msg", "fetch sts token success id : ", accessKeyID)
		}

	} else {
		c.lock.Lock()
		c.lastRetryFailCount++
		c.lock.Unlock()
		level.Warn(Logger).Log("msg", "fetch sts token error : ", err.Error())
	}
	return err
}

func (c *TokenAutoUpdateClient) processError(err error) (retry bool) {
	if err == nil {
		return false
	}
	if IsTokenError(err) {
		if fetchErr := c.fetchSTSToken(); fetchErr != nil {
			level.Warn(Logger).Log("msg", "operation error : ", err.Error(), "fetch sts token error : ", fetchErr.Error())
			// if fetch error, return false
			return false
		}
		return true
	}
	return false

}

func (c *TokenAutoUpdateClient) SetUserAgent(userAgent string) {
	c.logClient.SetUserAgent(userAgent)
}

// SetHTTPClient set a custom http client, all request will send to sls by this client
func (c *TokenAutoUpdateClient) SetHTTPClient(client *http.Client) {
	c.logClient.SetHTTPClient(client)
}

// SetRetryTimeout set retry timeout
func (c *TokenAutoUpdateClient) SetRetryTimeout(timeout time.Duration) {
	c.logClient.SetRetryTimeout(timeout)
}

// SetAuthVersion set auth version that the client used
func (c *TokenAutoUpdateClient) SetAuthVersion(version AuthVersionType) {
	c.logClient.SetAuthVersion(version)
}

// SetRegion set a region, must be set if using signature version v4
func (c *TokenAutoUpdateClient) SetRegion(region string) {
	c.logClient.SetRegion(region)
}

func (c *TokenAutoUpdateClient) Close() error {
	c.closeFlag = true
	return nil
}

func (c *TokenAutoUpdateClient) ResetAccessKeyToken(accessKeyID, accessKeySecret, securityToken string) {
	c.logClient.ResetAccessKeyToken(accessKeyID, accessKeySecret, securityToken)
}

func (c *TokenAutoUpdateClient) CreateProject(name, description string) (prj *LogProject, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		prj, err = c.logClient.CreateProject(name, description)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateProjectV2(name, description, dataRedundancyType string) (prj *LogProject, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		prj, err = c.logClient.CreateProjectV2(name, description, dataRedundancyType)
		if !c.processError(err) {
			return
		}
	}
	return
}

// UpdateProject create a new loghub project.
func (c *TokenAutoUpdateClient) UpdateProject(name, description string) (prj *LogProject, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		prj, err = c.logClient.UpdateProject(name, description)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetProject(name string) (prj *LogProject, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		prj, err = c.logClient.GetProject(name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListProject() (projectNames []string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		projectNames, err = c.logClient.ListProject()
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListProjectV2(offset, size int) (projects []LogProject, count, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		projects, count, total, err = c.logClient.ListProjectV2(offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CheckProjectExist(name string) (ok bool, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ok, err = c.logClient.CheckProjectExist(name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteProject(name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteProject(name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListLogStore(project string) (logstoreList []string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		logstoreList, err = c.logClient.ListLogStore(project)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListLogStoreV2(project string, offset, size int, telemetryType string) (logstoreList []string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		logstoreList, err = c.logClient.ListLogStoreV2(project, offset, size, telemetryType)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogStore(project string, logstore string) (logstoreRst *LogStore, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		logstoreRst, err = c.logClient.GetLogStore(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateLogStore(project string, logstore string, ttl, shardCnt int, autoSplit bool, maxSplitShard int) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateLogStore(project, logstore, ttl, shardCnt, autoSplit, maxSplitShard)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateLogStoreV2(project string, logstore *LogStore) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateLogStoreV2(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteLogStore(project string, logstore string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteLogStore(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateLogStore(project string, logstore string, ttl, shardCnt int) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateLogStore(project, logstore, ttl, shardCnt)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateLogStoreV2(project string, logstore *LogStore) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateLogStoreV2(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListMachineGroup(project string, offset, size int) (m []string, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		m, total, err = c.logClient.ListMachineGroup(project, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogStoreMeteringMode(project string, logstore string) (res *GetMeteringModeResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		res, err = c.logClient.GetLogStoreMeteringMode(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateLogStoreMeteringMode(project string, logstore string, meteringMode string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateLogStoreMeteringMode(project, logstore, meteringMode)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListMachines(project, machineGroupName string) (ms []*Machine, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ms, total, err = c.logClient.ListMachines(project, machineGroupName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListMachinesV2(project, machineGroupName string, offset, size int) (ms []*Machine, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ms, total, err = c.logClient.ListMachinesV2(project, machineGroupName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CheckLogstoreExist(project string, logstore string) (ok bool, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ok, err = c.logClient.CheckLogstoreExist(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CheckMachineGroupExist(project string, machineGroup string) (ok bool, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ok, err = c.logClient.CheckMachineGroupExist(project, machineGroup)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetMachineGroup(project string, machineGroup string) (m *MachineGroup, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		m, err = c.logClient.GetMachineGroup(project, machineGroup)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateMachineGroup(project string, m *MachineGroup) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateMachineGroup(project, m)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateMachineGroup(project string, m *MachineGroup) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateMachineGroup(project, m)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteMachineGroup(project string, machineGroup string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteMachineGroup(project, machineGroup)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateMetricConfig(project string, metricStore string, metricConfig *MetricsConfig) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateMetricConfig(project, metricStore, metricConfig)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteMetricConfig(project string, metricStore string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteMetricConfig(project, metricStore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetMetricConfig(project string, metricStore string) (metricConfig *MetricsConfig, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		metricConfig, err = c.logClient.GetMetricConfig(project, metricStore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateMetricConfig(project string, metricStore string, metricConfig *MetricsConfig) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateMetricConfig(project, metricStore, metricConfig)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListConfig(project string, offset, size int) (cfgNames []string, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		cfgNames, total, err = c.logClient.ListConfig(project, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CheckConfigExist(project string, config string) (ok bool, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ok, err = c.logClient.CheckConfigExist(project, config)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetConfig(project string, config string) (logConfig *LogConfig, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		logConfig, err = c.logClient.GetConfig(project, config)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateConfig(project string, config *LogConfig) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateConfig(project, config)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateConfig(project string, config *LogConfig) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateConfig(project, config)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteConfig(project string, config string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteConfig(project, config)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetAppliedMachineGroups(project string, confName string) (groupNames []string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		groupNames, err = c.logClient.GetAppliedMachineGroups(project, confName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetAppliedConfigs(project string, groupName string) (confNames []string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		confNames, err = c.logClient.GetAppliedConfigs(project, groupName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ApplyConfigToMachineGroup(project string, confName, groupName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.ApplyConfigToMachineGroup(project, confName, groupName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) RemoveConfigFromMachineGroup(project string, confName, groupName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.RemoveConfigFromMachineGroup(project, confName, groupName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateETL(project string, etljob ETL) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateETL(project, etljob)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateETL(project string, etljob ETL) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateETL(project, etljob)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetETL(project string, etlName string) (ETLJob *ETL, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ETLJob, err = c.logClient.GetETL(project, etlName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListETL(project string, offset int, size int) (ETLResponse *ListETLResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ETLResponse, err = c.logClient.ListETL(project, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteETL(project string, etlName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteETL(project, etlName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) StartETL(project string, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.StartETL(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) StopETL(project string, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.StopETL(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) RestartETL(project string, etljob ETL) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.RestartETL(project, etljob)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateEtlMeta(project string, etlMeta *EtlMeta) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateEtlMeta(project, etlMeta)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateEtlMeta(project string, etlMeta *EtlMeta) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateEtlMeta(project, etlMeta)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteEtlMeta(project string, etlMetaName, etlMetaKey string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteEtlMeta(project, etlMetaName, etlMetaKey)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetEtlMeta(project string, etlMetaName, etlMetaKey string) (etlMeta *EtlMeta, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		etlMeta, err = c.logClient.GetEtlMeta(project, etlMetaName, etlMetaKey)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListEtlMeta(project string, etlMetaName string, offset, size int) (total int, count int, etlMetaList []*EtlMeta, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		total, count, etlMetaList, err = c.logClient.ListEtlMeta(project, etlMetaName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListEtlMetaWithTag(project string, etlMetaName, etlMetaTag string, offset, size int) (total int, count int, etlMetaList []*EtlMeta, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		total, count, etlMetaList, err = c.logClient.ListEtlMetaWithTag(project, etlMetaName, etlMetaTag, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListEtlMetaName(project string, offset, size int) (total int, count int, etlMetaNameList []string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		total, count, etlMetaNameList, err = c.logClient.ListEtlMetaName(project, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListShards(project, logstore string) (shardIDs []*Shard, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		shardIDs, err = c.logClient.ListShards(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) SplitShard(project, logstore string, shardID int, splitKey string) (shards []*Shard, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		shards, err = c.logClient.SplitShard(project, logstore, shardID, splitKey)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) SplitNumShard(project, logstore string, shardID, shardNum int) (shards []*Shard, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		shards, err = c.logClient.SplitNumShard(project, logstore, shardID, shardNum)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) MergeShards(project, logstore string, shardID int) (shards []*Shard, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		shards, err = c.logClient.MergeShards(project, logstore, shardID)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PutLogs(project, logstore string, lg *LogGroup) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PutLogs(project, logstore, lg)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PutLogsWithMetricStoreURL(project, logstore string, lg *LogGroup) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PutLogsWithMetricStoreURL(project, logstore, lg)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PostLogStoreLogs(project, logstore string, lg *LogGroup, hashKey *string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PostLogStoreLogs(project, logstore, lg, hashKey)
		if !c.processError(err) {
			return
		}
	}
	return
}

// PostRawLogWithCompressType put raw log data to log service, no marshal
func (c *TokenAutoUpdateClient) PostRawLogWithCompressType(project, logstore string, rawLogData []byte, compressType int, hashKey *string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PostRawLogWithCompressType(project, logstore, rawLogData, compressType, hashKey)
		if !c.processError(err) {
			return
		}
	}
	return
}

// PutRawLogWithCompressType put raw log data to log service, no marshal
func (c *TokenAutoUpdateClient) PutRawLogWithCompressType(project, logstore string, rawLogData []byte, compressType int) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PutRawLogWithCompressType(project, logstore, rawLogData, compressType)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PutLogsWithCompressType(project, logstore string, lg *LogGroup, compressType int) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PutLogsWithCompressType(project, logstore, lg, compressType)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetCursor(project, logstore string, shardID int, from string) (cursor string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		cursor, err = c.logClient.GetCursor(project, logstore, shardID, from)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetCursorTime(project, logstore string, shardID int, cursor string) (cursorTime time.Time, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		cursorTime, err = c.logClient.GetCursorTime(project, logstore, shardID, cursor)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsBytes(project, logstore string, shardID int, cursor, endCursor string,
	logGroupMaxCount int) (out []byte, nextCursor string, err error) {
	plr := &PullLogRequest{
		Project:          project,
		Logstore:         logstore,
		ShardID:          shardID,
		Cursor:           cursor,
		EndCursor:        endCursor,
		LogGroupMaxCount: logGroupMaxCount,
	}
	return c.GetLogsBytesV2(plr)
}

// Deprecated: use GetLogsBytesWithQuery instead
func (c *TokenAutoUpdateClient) GetLogsBytesV2(plr *PullLogRequest) (out []byte, nextCursor string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		out, nextCursor, err = c.logClient.GetLogsBytesV2(plr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsBytesWithQuery(plr *PullLogRequest) (out []byte, plm *PullLogMeta, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		out, plm, err = c.logClient.GetLogsBytesWithQuery(plr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PullLogs(project, logstore string, shardID int, cursor, endCursor string,
	logGroupMaxCount int) (gl *LogGroupList, nextCursor string, err error) {
	plr := &PullLogRequest{
		Project:          project,
		Logstore:         logstore,
		ShardID:          shardID,
		Cursor:           cursor,
		EndCursor:        endCursor,
		LogGroupMaxCount: logGroupMaxCount,
	}
	return c.PullLogsV2(plr)
}

// Deprecated: use PullLogsWithQuery instead
func (c *TokenAutoUpdateClient) PullLogsV2(plr *PullLogRequest) (gl *LogGroupList, nextCursor string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		gl, nextCursor, err = c.logClient.PullLogsV2(plr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PullLogsWithQuery(plr *PullLogRequest) (gl *LogGroupList, plm *PullLogMeta, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		gl, plm, err = c.logClient.PullLogsWithQuery(plr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetHistograms(project, logstore string, topic string, from int64, to int64, queryExp string) (h *GetHistogramsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		h, err = c.logClient.GetHistograms(project, logstore, topic, from, to, queryExp)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetHistogramsV2(project, logstore string, ghr *GetHistogramRequest) (h *GetHistogramsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		h, err = c.logClient.GetHistogramsV2(project, logstore, ghr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetHistogramsToCompleted(project, logstore string, topic string, from int64, to int64, queryExp string) (h *GetHistogramsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		h, err = c.logClient.GetHistogramsToCompleted(project, logstore, topic, from, to, queryExp)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetHistogramsToCompletedV2(project, logstore string, ghr *GetHistogramRequest) (h *GetHistogramsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		h, err = c.logClient.GetHistogramsToCompletedV2(project, logstore, ghr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsV2(project, logstore string, req *GetLogRequest) (r *GetLogsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogsV2(project, logstore, req)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsV3(project, logstore string, req *GetLogRequest) (r *GetLogsV3Response, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogsV3(project, logstore, req)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsToCompletedV2(project, logstore string, req *GetLogRequest) (r *GetLogsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogsToCompletedV2(project, logstore, req)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsToCompletedV3(project, logstore string, req *GetLogRequest) (r *GetLogsV3Response, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogsToCompletedV3(project, logstore, req)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogLinesV2(project, logstore string, req *GetLogRequest) (r *GetLogLinesResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogLinesV2(project, logstore, req)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogs(project, logstore string, topic string, from int64, to int64, queryExp string,
	maxLineNum int64, offset int64, reverse bool) (r *GetLogsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogs(project, logstore, topic, from, to, queryExp, maxLineNum, offset, reverse)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsByNano(project, logstore string, topic string, fromInNs int64, toInNs int64, queryExp string,
	maxLineNum int64, offset int64, reverse bool) (r *GetLogsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogsByNano(project, logstore, topic, fromInNs, toInNs, queryExp, maxLineNum, offset, reverse)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogsToCompleted(project, logstore string, topic string, from int64, to int64, queryExp string,
	maxLineNum int64, offset int64, reverse bool) (r *GetLogsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogsToCompleted(project, logstore, topic, from, to, queryExp, maxLineNum, offset, reverse)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogLines(project, logstore string, topic string, from int64, to int64, queryExp string,
	maxLineNum int64, offset int64, reverse bool) (r *GetLogLinesResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogLines(project, logstore, topic, from, to, queryExp, maxLineNum, offset, reverse)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetLogLinesByNano(project, logstore string, topic string, fromInNs int64, toInNS int64, queryExp string,
	maxLineNum int64, offset int64, reverse bool) (r *GetLogLinesResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		r, err = c.logClient.GetLogLinesByNano(project, logstore, topic, fromInNs, toInNS, queryExp, maxLineNum, offset, reverse)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateIndex(project, logstore string, index Index) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateIndex(project, logstore, index)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateIndex(project, logstore string, index Index) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateIndex(project, logstore, index)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteIndex(project, logstore string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteIndex(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetIndex(project, logstore string) (index *Index, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		index, err = c.logClient.GetIndex(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListDashboard(project string, dashboardName string, offset, size int) (dashboardList []string, count, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		dashboardList, count, total, err = c.logClient.ListDashboard(project, dashboardName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListDashboardV2(project string, dashboardName string, offset, size int) (dashboardList []string, dashboardItems []ResponseDashboardItem, count, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		dashboardList, dashboardItems, count, total, err = c.logClient.ListDashboardV2(project, dashboardName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetDashboard(project, name string) (dashboard *Dashboard, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		dashboard, err = c.logClient.GetDashboard(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) DeleteDashboard(project, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteDashboard(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) UpdateDashboard(project string, dashboard Dashboard) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateDashboard(project, dashboard)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) CreateDashboard(project string, dashboard Dashboard) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateDashboard(project, dashboard)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) GetChart(project, dashboardName, chartName string) (chart *Chart, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		chart, err = c.logClient.GetChart(project, dashboardName, chartName)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) DeleteChart(project, dashboardName, chartName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteChart(project, dashboardName, chartName)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) UpdateChart(project, dashboardName string, chart Chart) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateChart(project, dashboardName, chart)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) CreateChart(project, dashboardName string, chart Chart) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateChart(project, dashboardName, chart)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateSavedSearch(project string, savedSearch *SavedSearch) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateSavedSearch(project, savedSearch)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateSavedSearch(project string, savedSearch *SavedSearch) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateSavedSearch(project, savedSearch)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteSavedSearch(project string, savedSearchName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteSavedSearch(project, savedSearchName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetSavedSearch(project string, savedSearchName string) (savedSearch *SavedSearch, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		savedSearch, err = c.logClient.GetSavedSearch(project, savedSearchName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListSavedSearch(project string, savedSearchName string, offset, size int) (savedSearches []string, total int, count int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		savedSearches, total, count, err = c.logClient.ListSavedSearch(project, savedSearchName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListSavedSearchV2(project string, savedSearchName string, offset, size int) (savedSearches []string, savedsearchItems []ResponseSavedSearchItem, total int, count int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		savedSearches, savedsearchItems, total, count, err = c.logClient.ListSavedSearchV2(project, savedSearchName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateAlert(project string, alert *Alert) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateAlert(project, alert)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateAlert(project string, alert *Alert) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateAlert(project, alert)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteAlert(project string, alertName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteAlert(project, alertName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetAlert(project string, alertName string) (alert *Alert, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		alert, err = c.logClient.GetAlert(project, alertName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DisableAlert(project string, alertName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DisableAlert(project, alertName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) EnableAlert(project string, alertName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.EnableAlert(project, alertName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListAlert(project string, alertName string, dashboard string, offset, size int) (alerts []*Alert, total int, count int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		alerts, total, count, err = c.logClient.ListAlert(project, alertName, dashboard, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateAlertString(project string, alert string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateAlertString(project, alert)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) UpdateAlertString(project string, alertName, alert string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateAlertString(project, alertName, alert)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) GetAlertString(project string, alertName string) (alert string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		alert, err = c.logClient.GetAlertString(project, alertName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateDashboardString(project string, dashboardStr string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateDashboardString(project, dashboardStr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateDashboardString(project string, dashboardName, dashboardStr string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateDashboardString(project, dashboardName, dashboardStr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetDashboardString(project, name string) (dashboard string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		dashboard, err = c.logClient.GetDashboardString(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetConfigString(project string, config string) (logConfig string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		logConfig, err = c.logClient.GetConfigString(project, config)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateConfigString(project string, config string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateConfigString(project, config)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateConfigString(project string, configName, configDetail string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateConfigString(project, configName, configDetail)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateIndexString(project, logstore string, index string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateIndexString(project, logstore, index)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateIndexString(project, logstore string, index string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateIndexString(project, logstore, index)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetIndexString(project, logstore string) (index string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		index, err = c.logClient.GetIndexString(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateConsumerGroup(project, logstore string, cg ConsumerGroup) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateConsumerGroup(project, logstore, cg)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateConsumerGroup(project, logstore string, cg ConsumerGroup) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateConsumerGroup(project, logstore, cg)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteConsumerGroup(project, logstore string, cgName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteConsumerGroup(project, logstore, cgName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListConsumerGroup(project, logstore string) (cgList []*ConsumerGroup, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		cgList, err = c.logClient.ListConsumerGroup(project, logstore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) HeartBeat(project, logstore string, cgName, consumer string, heartBeatShardIDs []int) (shardIDs []int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		shardIDs, err = c.logClient.HeartBeat(project, logstore, cgName, consumer, heartBeatShardIDs)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateCheckpoint(project, logstore string, cgName string, consumer string, shardID int, checkpoint string, forceSuccess bool) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateCheckpoint(project, logstore, cgName, consumer, shardID, checkpoint, forceSuccess)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetCheckpoint(project, logstore string, cgName string) (checkPointList []*ConsumerGroupCheckPoint, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		checkPointList, err = c.logClient.GetCheckpoint(project, logstore, cgName)
		if !c.processError(err) {
			return
		}
	}
	return
}

// ####################### Resource Tags API ######################
// TagResources tag specific resource
func (c *TokenAutoUpdateClient) TagResources(project string, tags *ResourceTags) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.TagResources(project, tags)
		if !c.processError(err) {
			return
		}
	}
	return
}

// TagResourcesSystemTags tag specific resource
func (c *TokenAutoUpdateClient) TagResourcesSystemTags(project string, tags *ResourceSystemTags) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.TagResourcesSystemTags(project, tags)
		if !c.processError(err) {
			return
		}
	}
	return
}

// UnTagResources untag specific resource
func (c *TokenAutoUpdateClient) UnTagResources(project string, tags *ResourceUnTags) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UnTagResources(project, tags)
		if !c.processError(err) {
			return
		}
	}
	return
}

// UnTagResourcesSystemTags untag specific resource
func (c *TokenAutoUpdateClient) UnTagResourcesSystemTags(project string, tags *ResourceUnSystemTags) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UnTagResourcesSystemTags(project, tags)
		if !c.processError(err) {
			return
		}
	}
	return
}

// ListTagResources list tag resources
func (c *TokenAutoUpdateClient) ListTagResources(project string,
	resourceType string,
	resourceIDs []string,
	tags []ResourceFilterTag,
	nextToken string) (respTags []*ResourceTagResponse, respNextToken string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		respTags, respNextToken, err = c.logClient.ListTagResources(project, resourceType, resourceIDs, tags, nextToken)
		if !c.processError(err) {
			return
		}
	}
	return
}

// ListSystemTagResources list system tag resources
func (c *TokenAutoUpdateClient) ListSystemTagResources(project string,
	resourceType string,
	resourceIDs []string,
	tags []ResourceFilterTag,
	tagOwnerUid string,
	category string,
	scope string,
	nextToken string) (respTags []*ResourceTagResponse, respNextToken string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		respTags, respNextToken, err = c.logClient.ListSystemTagResources(project, resourceType, resourceIDs, tags, tagOwnerUid, category, scope, nextToken)
		if !c.processError(err) {
			return
		}
	}
	return
}

// ####################### Scheduled SQL API ######################
func (c *TokenAutoUpdateClient) CreateScheduledSQL(project string, scheduledsql *ScheduledSQL) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateScheduledSQL(project, scheduledsql)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteScheduledSQL(project string, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteScheduledSQL(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateScheduledSQL(project string, scheduledsql *ScheduledSQL) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateScheduledSQL(project, scheduledsql)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetScheduledSQL(project string, name string) (s *ScheduledSQL, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		s, err = c.logClient.GetScheduledSQL(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListScheduledSQL(project, name, displayName string, offset, size int) (scheduledsqls []*ScheduledSQL, total, count int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		scheduledsqls, total, count, err = c.logClient.ListScheduledSQL(project, name, displayName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetScheduledSQLJobInstance(projectName, jobName, instanceId string, result bool) (instance *ScheduledSQLJobInstance, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		instance, err = c.logClient.GetScheduledSQLJobInstance(projectName, jobName, instanceId, result)
		if !c.processError(err) {
			return
		}
	}
	return instance, err
}

func (c *TokenAutoUpdateClient) ModifyScheduledSQLJobInstanceState(projectName, jobName, instanceId string, state ScheduledSQLState) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.ModifyScheduledSQLJobInstanceState(projectName, jobName, instanceId, state)
		if !c.processError(err) {
			return
		}
	}
	return err
}

func (c *TokenAutoUpdateClient) ListScheduledSQLJobInstances(projectName, jobName string, status *InstanceStatus) (instances []*ScheduledSQLJobInstance, total, count int64, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		instances, total, count, err = c.logClient.ListScheduledSQLJobInstances(projectName, jobName, status)
		if !c.processError(err) {
			return
		}
	}
	return instances, total, count, err
}

// ####################### Resource API ######################
func (c *TokenAutoUpdateClient) ListResource(resourceType string, resourceName string, offset, size int) (resourceList []*Resource, count, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		resourceList, count, total, err = c.logClient.ListResource(resourceType, resourceName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetResource(name string) (resource *Resource, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		resource, err = c.logClient.GetResource(name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetResourceString(name string) (resource string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		resource, err = c.logClient.GetResourceString(name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteResource(name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteResource(name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateResource(resource *Resource) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateResource(resource)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateResourceString(resourceName, resourceStr string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateResourceString(resourceName, resourceStr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateResource(resource *Resource) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateResource(resource)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateResourceString(resourceStr string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateResourceString(resourceStr)
		if !c.processError(err) {
			return
		}
	}
	return
}

// ####################### Resource Record API ######################
func (c *TokenAutoUpdateClient) ListResourceRecord(resourceName string, offset, size int) (recordList []*ResourceRecord, count, total int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		recordList, count, total, err = c.logClient.ListResourceRecord(resourceName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetResourceRecord(resourceName, recordId string) (record *ResourceRecord, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		record, err = c.logClient.GetResourceRecord(resourceName, recordId)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetResourceRecordString(resourceName, name string) (record string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		record, err = c.logClient.GetResourceRecordString(resourceName, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteResourceRecord(resourceName, recordId string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteResourceRecord(resourceName, recordId)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateResourceRecord(resourceName string, record *ResourceRecord) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateResourceRecord(resourceName, record)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateResourceRecordString(resourceName, recordStr string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateResourceString(resourceName, recordStr)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateResourceRecord(resourceName string, record *ResourceRecord) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateResourceRecord(resourceName, record)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateResourceRecordString(resourceName, recordStr string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateResourceRecordString(resourceName, recordStr)
		if !c.processError(err) {
			return
		}
	}
	return
}

// ####################### Ingestion API ######################
func (c *TokenAutoUpdateClient) CreateIngestion(project string, ingestion *Ingestion) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateIngestion(project, ingestion)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateIngestion(project string, ingestion *Ingestion) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateIngestion(project, ingestion)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetIngestion(project string, name string) (ingestion *Ingestion, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ingestion, err = c.logClient.GetIngestion(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListIngestion(project, logstore, name, displayName string, offset, size int) (ingestions []*Ingestion, total, count int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		ingestions, total, count, err = c.logClient.ListIngestion(project, logstore, name, displayName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteIngestion(project string, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteIngestion(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateExport(project string, export *Export) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateExport(project, export)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) UpdateExport(project string, export *Export) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateExport(project, export)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) GetExport(project, name string) (export *Export, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		export, err = c.logClient.GetExport(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) ListExport(project, logstore, name, displayName string, offset, size int) (exports []*Export, total, count int, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		exports, total, count, err = c.logClient.ListExport(project, logstore, name, displayName, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) DeleteExport(project string, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteExport(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) RestartExport(project string, export *Export) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.RestartExport(project, export)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) CreateMetricStore(project string, metricStore *LogStore) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateMetricStore(project, metricStore)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) UpdateMetricStore(project string, metricStore *LogStore) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateMetricStore(project, metricStore)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) DeleteMetricStore(project, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteMetricStore(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}
func (c *TokenAutoUpdateClient) GetMetricStore(project, name string) (metricStore *LogStore, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		metricStore, err = c.logClient.GetMetricStore(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateProjectPolicy(project, policy string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateProjectPolicy(project, policy)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteProjectPolicy(project string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteProjectPolicy(project)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetProjectPolicy(project string) (policy string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		policy, err = c.logClient.GetProjectPolicy(project)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PublishAlertEvent(project string, alertResult []byte) error {
	var err error = nil
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PublishAlertEvent(project, alertResult)
		if err == nil {
			break
		}
	}
	return err
}

func (c *TokenAutoUpdateClient) CreateEventStore(project string, eventStore *LogStore) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateEventStore(project, eventStore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateEventStore(project string, eventStore *LogStore) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateEventStore(project, eventStore)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteEventStore(project, name string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteEventStore(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetEventStore(project, name string) (eventStore *LogStore, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		eventStore, err = c.logClient.GetEventStore(project, name)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListEventStore(project string, offset, size int) (eventStores []string, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		eventStores, err = c.logClient.ListEventStore(project, offset, size)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) PostLogStoreLogsV2(project, logstore string, req *PostLogStoreLogsRequest) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.PostLogStoreLogsV2(project, logstore, req)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) CreateStoreView(project string, storeView *StoreView) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.CreateStoreView(project, storeView)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) UpdateStoreView(project string, storeView *StoreView) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.UpdateStoreView(project, storeView)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) DeleteStoreView(project string, storeViewName string) (err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		err = c.logClient.DeleteStoreView(project, storeViewName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetStoreView(project string, storeViewName string) (storeView *StoreView, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		storeView, err = c.logClient.GetStoreView(project, storeViewName)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) ListStoreViews(project string, req *ListStoreViewsRequest) (resp *ListStoreViewsResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		resp, err = c.logClient.ListStoreViews(project, req)
		if !c.processError(err) {
			return
		}
	}
	return
}

func (c *TokenAutoUpdateClient) GetStoreViewIndex(project string, storeViewName string) (resp *GetStoreViewIndexResponse, err error) {
	for i := 0; i < c.maxTryTimes; i++ {
		resp, err = c.logClient.GetStoreViewIndex(project, storeViewName)
		if !c.processError(err) {
			return
		}
	}
	return
}

package pilot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	log "github.com/sirupsen/logrus"
)

const FLUENTD_CONF_HOME = "/etc/fluentd"
const LABEL_LOGS = "logtopic"

type containerLogInfo struct {
	containerId string
	logPath     string
	logTopic    string
}

var containerLogs = make(map[string]containerLogInfo)

type Pilot struct {
	tpl          *template.Template
	dockerClient *client.Client
	reloadable   bool
}

func New(tplStr string) (*Pilot, error) {
	tpl, err := template.New("fluentd").Parse(tplStr)
	if err != nil {
		return nil, fmt.Errorf("模板解析失败：%s", err.Error())
	}

	client, err := client.NewEnvClient()
	if err != nil {
		return nil, fmt.Errorf("创建容器客户端失败：%s", err.Error())
	}

	return &Pilot{
		dockerClient: client,
		tpl:          tpl,
	}, nil
}

func (p *Pilot) watch() error {
	p.reloadable = false
	if err := p.processAllContainers(); err != nil {
		return err
	}
	//StartFluentd()
	p.reloadable = true

	ctx := context.Background()
	filter := filters.NewArgs()
	filter.Add("type", "container")

	options := types.EventsOptions{
		Filters: filter,
	}
	msgs, errs := p.client().Events(ctx, options)
	for {
		select {
		case msg := <-msgs:
			if err := p.processEvent(msg); err != nil {
				log.Warnf("处理容器事件失败: %v,  %v", msg, err)
			}
		case err := <-errs:
			log.Warnf("容器事件错误: %v", err)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			} else {
				msgs, errs = p.client().Events(ctx, options)
			}
		}
	}
}

type Source struct {
	Application string
	Service     string
	POD         string
	Container   string
}

type LogConfig struct {
	Name         string
	HostDir      string
	ContainerDir string
	Format       string
	FormatConfig map[string]string
	File         string
	Tags         map[string]string
	Target       string
	TimeKey      string
}

func (p *Pilot) cleanConfigs() error {
	confDir := fmt.Sprintf("%s/conf.d", FLUENTD_CONF_HOME)
	d, err := os.Open(confDir)
	if err != nil {
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}

	for _, name := range names {
		path := filepath.Join(confDir, name)
		stat, err := os.Stat(filepath.Join(confDir, name))
		if err != nil {
			return err
		}
		if stat.Mode().IsRegular() {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Pilot) processAllContainers() error {
	opts := types.ContainerListOptions{All: true}
	containers, err := p.client().ContainerList(context.Background(), opts)
	if err != nil {
		return fmt.Errorf("获取容器列表失败：%s", err.Error())
	}

	//clean config
	if err := p.cleanConfigs(); err != nil {
		return fmt.Errorf("清理老的配置文件失败：%s", err.Error())
	}

	for _, c := range containers {
		if c.State == "removing" {
			continue
		}
		containerJSON, err := p.client().ContainerInspect(context.Background(), c.ID)
		if err != nil {
			return fmt.Errorf("获取容器信息失败：%s, %s", c.ID, err.Error())
		}

		p.newContainer(containerJSON)
	}

	return nil
}

func (p *Pilot) newContainer(container types.ContainerJSON) {
	if container.State.Status != "running" {
		return
	}

	// 对一个 POD 上加的标签，在 /pause 容器上出现
	if container.Path == "/pause" {
		topic := ""
		for key, value := range container.Config.Labels {
			if key == LABEL_LOGS {
				topic = value
			}
		}
		if c, ok := containerLogs[container.Name]; ok {
			if topic != "" {
				c.logTopic = topic
				p.newContainerLog(&c)
			}
			delete(containerLogs, container.Name)
		} else {
			containerLogs[container.Name] = containerLogInfo{logTopic: topic}
		}
	} else {
		if c, ok := containerLogs[container.Name]; ok {
			if c.logTopic != "" {
				c.containerId = container.ID
				c.logPath = container.LogPath
				p.newContainerLog(&c)
				delete(containerLogs, container.Name)
			}
			delete(containerLogs, container.Name)
		} else {
			containerLogs[container.Name] = containerLogInfo{containerId: container.ID, logPath: container.LogPath}
		}
	}
}

func (p *Pilot) newContainerLog(logInfo *containerLogInfo) error {
	fluentdConfig, err := p.render(logInfo)
	if err != nil {
		return err
	}
	if err = ioutil.WriteFile(p.pathOf(logInfo.containerId), []byte(fluentdConfig), os.FileMode(0644)); err != nil {
		return err
	}
	//p.tryReload()
	return nil
}

func (p *Pilot) pathOf(container string) string {
	return fmt.Sprintf("%s/conf.d/%s.conf", FLUENTD_CONF_HOME, container)
}

func (p *Pilot) delContainer(id string) error {
	log.Infof("删除容器配置 %s", id)
	if err := os.Remove(p.pathOf(id)); err != nil {
		return err
	}
	return nil
}

func (p *Pilot) client() *client.Client {
	return p.dockerClient
}

func (p *Pilot) processEvent(msg events.Message) error {
	containerId := msg.Actor.ID
	ctx := context.Background()
	switch msg.Action {
	case "running":
		log.Debugf("处理容器 running 事件: %s", containerId)
		if p.exists(containerId) {
			log.Debugf("%s 已经存在", containerId)
			return nil
		}
		containerJSON, err := p.client().ContainerInspect(ctx, containerId)
		if err != nil {
			return fmt.Errorf("获取容器信息失败：%s, %s", containerId, err.Error())
		}

		p.newContainer(containerJSON)
	case "destroy":
		p.delContainer(containerId)
	}
	return nil
}

func (p *Pilot) exists(containId string) bool {
	if _, err := os.Stat(p.pathOf(containId)); os.IsNotExist(err) {
		return false
	}
	return true
}

func (p *Pilot) render(logInfo *containerLogInfo) (string, error) {
	var buf bytes.Buffer

	context := map[string]interface{}{
		"logPath":  logInfo.logPath,
		"logTopic": logInfo.logTopic,
	}
	if err := p.tpl.Execute(&buf, context); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func Run(tpl string) error {
	p, err := New(tpl)
	if err != nil {
		return err
	}
	return p.watch()
}

/*
Copyright 2021 Loggie Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/loggie-io/loggie/pkg/core/cfg"
	"github.com/loggie-io/loggie/pkg/core/log"
	"github.com/loggie-io/loggie/pkg/core/source"
	logconfigv1beta1 "github.com/loggie-io/loggie/pkg/discovery/kubernetes/apis/loggie/v1beta1"
	"github.com/loggie-io/loggie/pkg/discovery/kubernetes/client/listers/loggie/v1beta1"
	"github.com/loggie-io/loggie/pkg/discovery/kubernetes/helper"
	"github.com/loggie-io/loggie/pkg/discovery/kubernetes/index"
	"github.com/loggie-io/loggie/pkg/pipeline"
	"github.com/loggie-io/loggie/pkg/source/file"
	"github.com/loggie-io/loggie/pkg/util"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"strings"
)

const (
	GenerateConfigName           = "kube-loggie.yml"
	GenerateTypeLoggieConfigName = "loggie-config.yml"
	GenerateTypeNodeConfigName   = "node-config.yml"
)

type fileSource struct {
	ContainerName string       `yaml:"containerName,omitempty"`
	MatchFields   *matchFields `yaml:"matchFields,omitempty"`
	cfg.CommonCfg `yaml:",inline"`
}

func (f *fileSource) getSource() (*source.Config, error) {
	srccfg := &source.Config{}
	if err := cfg.Unpack(f.CommonCfg, srccfg); err != nil {
		return nil, err
	}
	return srccfg, nil
}

func (f *fileSource) setSource(s *source.Config) error {
	c, err := cfg.Pack(s)
	if err != nil {
		return err
	}
	f.CommonCfg = cfg.MergeCommonCfg(f.CommonCfg, c, true)
	return nil
}

func (f *fileSource) getFileConfig() (*file.Config, error) {
	conf := &file.Config{}
	if err := cfg.Unpack(f.CommonCfg, conf); err != nil {
		return nil, errors.WithMessage(err, "unpack file source config failed")
	}
	return conf, nil
}

func (f *fileSource) setFileConfig(c *file.Config) error {
	conf, err := cfg.Pack(c)
	if err != nil {
		return err
	}
	cfg.MergeCommonCfg(f.CommonCfg, conf, true)
	return nil
}

type matchFields struct {
	LabelKey      []string `yaml:"labelKey,omitempty"`
	AnnotationKey []string `yaml:"annotationKey,omitempty"`
	Env           []string `yaml:"env,omitempty"`
}

func (c *Controller) handleLogConfigTypePodAddOrUpdate(lgc *logconfigv1beta1.LogConfig) (err error, podsName []string) {

	// find pods related in the node
	podList, err := helper.GetLogConfigRelatedPod(lgc, c.podsLister)
	if err != nil {
		return err, nil
	}
	if len(podList) == 0 {
		log.Info("logConfig %s/%s matched pods is null", lgc.Namespace, lgc.Name)
		return nil, nil
	}

	var ret []string
	for _, pod := range podList {
		if err := c.handleLogConfigPerPod(lgc, pod); err != nil {
			return err, []string{pod.Name}
		}
		ret = append(ret, pod.Name)
	}

	return nil, ret
}

func (c *Controller) handlePodAddOrUpdate(pod *corev1.Pod) error {

	// check if pod is in the index
	if c.typePodIndex.IsPodExist(pod.Namespace, pod.Name) {
		log.Info("pod: %s/%s is in index, ignore pod addOrUpdate event", pod.Namespace, pod.Name)
		return nil
	}

	// find pod related logConfigs
	lgcList, err := helper.GetPodRelatedLogConfigs(pod, c.logConfigLister)
	if err != nil || len(lgcList) == 0 {
		return nil
	}

	for _, lgc := range lgcList {

		if !c.belongOfCluster(lgc.Spec.Selector.Cluster) {
			continue
		}

		if err := lgc.Validate(); err != nil {
			continue
		}

		if err := c.handleLogConfigPerPod(lgc, pod); err != nil {
			msg := fmt.Sprintf(MessageSyncFailed, lgc.Spec.Selector.Type, pod.Name, err.Error())
			c.record.Event(lgc, corev1.EventTypeWarning, ReasonFailed, msg)
			return err
		}
		log.Info("handle pod %s/%s addOrUpdate event and sync config file success, related logConfig is %s", pod.Namespace, pod.Name, lgc.Name)
		msg := fmt.Sprintf(MessageSyncSuccess, lgc.Spec.Selector.Type, pod.Name)
		c.record.Event(lgc, corev1.EventTypeNormal, ReasonSuccess, msg)
	}

	return nil
}

func (c *Controller) handleLogConfigPerPod(lgc *logconfigv1beta1.LogConfig, pod *corev1.Pod) error {

	// generate pod related pipeline configs
	pipeRaw, err := getConfigFromPodAndLogConfig(c.config, lgc, pod, c.sinkLister, c.interceptorLister)
	if err != nil {
		return err
	}
	if pipeRaw == nil {
		return nil
	}

	// validate pipeline configs
	pipeCopy, err := pipeRaw.DeepCopy()
	if err != nil {
		return errors.WithMessage(err, "deep copy pipeline config error")
	}
	pipeCopy.SetDefaults()
	if err := pipeCopy.Validate(); err != nil {
		return err
	}

	// get pod related pipeline configs from index
	cfgsInIndex := c.typePodIndex.GetPipeConfigs(pod.Namespace, pod.Name, lgc.Name)

	// compare and check if we should update
	// FIXME Array order may causes inequality
	if cmp.Equal(pipeRaw, cfgsInIndex) {
		return nil
	}

	// update index
	if err := c.typePodIndex.ValidateAndSetConfigs(pod.Namespace, pod.Name, lgc.Name, pipeRaw); err != nil {
		return err
	}

	// TODO merge pipelines if there is no specific pipeline configs
	err = c.syncConfigToFile(logconfigv1beta1.SelectorTypePod)
	if err != nil {
		return errors.WithMessage(err, "sync config to file failed")
	}
	// TODO update status when success
	log.Info("handle logConfig %s/%s addOrUpdate event and sync config file success, related pod: %s", lgc.Namespace, lgc.Name, pod.Name)

	return nil
}

func getConfigFromPodAndLogConfig(config *Config, lgc *logconfigv1beta1.LogConfig, pod *corev1.Pod,
	sinkLister v1beta1.SinkLister, interceptorLister v1beta1.InterceptorLister) (*pipeline.ConfigRaw, error) {

	if len(pod.Status.ContainerStatuses) == 0 {
		return nil, nil
	}
	cfgs, err := getConfigFromContainerAndLogConfig(config, lgc, pod, sinkLister, interceptorLister)
	if err != nil {
		return nil, err
	}
	return cfgs, nil
}

func getConfigFromContainerAndLogConfig(config *Config, lgc *logconfigv1beta1.LogConfig, pod *corev1.Pod,
	sinkLister v1beta1.SinkLister, interceptorLister v1beta1.InterceptorLister) (*pipeline.ConfigRaw, error) {

	logConf := lgc.DeepCopy()
	sourceConfList := make([]fileSource, 0)
	err := cfg.UnpackRaw([]byte(logConf.Spec.Pipeline.Sources), &sourceConfList)
	if err != nil {
		return nil, errors.WithMessagef(err, "unpack logConfig %s sources failed", lgc.Namespace)
	}

	filesources, err := updateSources(sourceConfList, config, pod, logConf.Name)
	if err != nil {
		return nil, err
	}
	pipecfg, err := toPipeConfig(lgc.Namespace, lgc.Name, logConf.Spec.Pipeline, filesources, sinkLister, interceptorLister)
	if err != nil {
		return nil, err
	}
	return pipecfg, nil
}

func updateSources(sourceConfList []fileSource, config *Config, pod *corev1.Pod, logConfigName string) ([]fileSource, error) {
	filesources := make([]fileSource, 0)
	for _, sourceConf := range sourceConfList {
		filesrc, err := getConfigPerSource(config, sourceConf, pod, logConfigName)
		if err != nil {
			return nil, err
		}
		filesources = append(filesources, filesrc...)
	}
	return filesources, nil
}

func getConfigPerSource(config *Config, s fileSource, pod *corev1.Pod, logconfigName string) ([]fileSource, error) {

	filesrcList := make([]fileSource, 0)
	for _, status := range pod.Status.ContainerStatuses {
		filesrc := fileSource{}
		if err := util.Clone(s, &filesrc); err != nil {
			return nil, err
		}
		statusContainerId := strings.Split(status.ContainerID, "//")
		if len(statusContainerId) < 2 {
			return nil, errors.Errorf("parse containerId in pod status err, status.ContainerID: %s", status.ContainerID)
		}
		containerId := statusContainerId[1]

		src, err := filesrc.getSource()
		if err != nil {
			return nil, err
		}
		if src.Type != file.Type {
			return nil, errors.New("only source type=file is supported when selector.type=pod")
		}

		if s.ContainerName != "" && s.ContainerName != status.Name {
			continue
		}
		// change the source name, add pod.Name-containerName as prefix
		src.Name = genTypePodSourceName(pod.Name, status.Name, src.Name)

		// inject default pod metadata
		if err = injectFields(config, s.MatchFields, src, pod, logconfigName, status.Name); err != nil {
			return nil, err
		}
		if err := filesrc.setSource(src); err != nil {
			return nil, err
		}

		// use paths of the node
		if err = updatePaths(config, &filesrc, pod, status.Name, containerId); err != nil {
			return nil, err
		}

		filesrcList = append(filesrcList, filesrc)
	}

	return filesrcList, nil
}

func genTypePodSourceName(podName string, containerName string, sourceName string) string {
	return fmt.Sprintf("%s/%s/%s", podName, containerName, sourceName)
}

func getTypePodOriginSourceName(podSourceName string) string {
	res := strings.Split(podSourceName, "/")
	if len(res) == 0 {
		return ""
	}
	return res[len(res)-1]
}

func updatePaths(config *Config, s *fileSource, pod *corev1.Pod, containerName, containerId string) error {
	filecfg, err := s.getFileConfig()
	if err != nil {
		return err
	}
	// update paths with real paths in the node
	nodePaths, err := getPathsInNode(config, filecfg.CollectConfig.Paths, pod, containerName, containerId)
	if err != nil {
		return err
	}

	filecfg.CollectConfig.Paths = nodePaths
	err = s.setFileConfig(filecfg)
	if err != nil {
		return err
	}
	return nil
}

func getPathsInNode(config *Config, containerPaths []string, pod *corev1.Pod, containerName string, containerId string) ([]string, error) {
	if len(containerPaths) == 0 {
		return nil, errors.New("path is empty")
	}

	var paths []string
	for _, p := range containerPaths {

		// container stdout logs
		if p == logconfigv1beta1.PathStdout {
			if config.ContainerRuntime == RuntimeDocker {
				paths = append(paths, helper.GenDockerStdoutLog(config.DockerDataRoot, containerId))
			} else {
				paths = append(paths, helper.GenContainerdStdoutLog(config.PodLogDirPrefix, pod.Namespace, pod.Name, string(pod.UID), containerName)...)
			}

			continue
		}

		// TODO find log files in docker rootfs
		p, err := helper.PathsInNode(config.KubeletRootDir, []string{p}, pod, containerName)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p...)
	}

	return paths, nil
}

func injectFields(config *Config, match *matchFields, src *source.Config, pod *corev1.Pod, lgcName string, containerName string) error {
	if src.Fields == nil {
		src.Fields = make(map[string]interface{})
	}

	m := config.Fields
	if m.Namespace != "" {
		src.Fields[m.Namespace] = pod.Namespace
	}
	if m.NodeName != "" {
		src.Fields[m.NodeName] = pod.Spec.NodeName
	}
	if m.PodName != "" {
		src.Fields[m.PodName] = pod.Name
	}
	if m.ContainerName != "" {
		src.Fields[m.ContainerName] = containerName
	}
	if m.LogConfig != "" {
		src.Fields[m.LogConfig] = lgcName
	}

	if match != nil {
		if len(match.LabelKey) > 0 {
			for k, v := range helper.GetMatchedPodLabel(match.LabelKey, pod) {
				src.Fields[k] = v
			}
		}
		if len(match.AnnotationKey) > 0 {
			for k, v := range helper.GetMatchedPodAnnotation(match.AnnotationKey, pod) {
				src.Fields[k] = v
			}
		}
		if len(match.Env) > 0 {
			for k, v := range helper.GetMatchedPodEnv(match.Env, pod, containerName) {
				src.Fields[k] = v
			}
		}
	}

	return nil
}

func toPipeConfig(lgcNamespace string, lgcName string, lgcPipe *logconfigv1beta1.Pipeline, filesources []fileSource, sinkLister v1beta1.SinkLister, interceptorLister v1beta1.InterceptorLister) (*pipeline.ConfigRaw, error) {
	pipecfg := &pipeline.ConfigRaw{}

	pipecfg.Name = fmt.Sprintf("%s/%s", lgcNamespace, lgcName)

	src, err := toPipelineSource(filesources)
	if err != nil {
		return pipecfg, err
	}
	pipecfg.Sources = src

	sink, err := helper.ToPipelineSink(lgcPipe.SinkRef, sinkLister)
	if err != nil {
		return pipecfg, err
	}
	pipecfg.Sink = sink

	interceptors, err := toPipelineInterceptorWithPodInject(lgcPipe.InterceptorRef, interceptorLister, filesources)
	if err != nil {
		return pipecfg, err
	}
	pipecfg.Interceptors = interceptors

	return pipecfg, nil
}

func toPipelineSource(filesources []fileSource) ([]cfg.CommonCfg, error) {
	sourceConfList := make([]cfg.CommonCfg, 0)

	for _, sr := range filesources {
		sourceConf, err := cfg.Pack(sr.CommonCfg)
		if err != nil {
			return nil, err
		}

		sourceConfList = append(sourceConfList, sourceConf)
	}

	return sourceConfList, nil
}

func toPipelineInterceptorWithPodInject(interceptorRef string, interceptorLister v1beta1.InterceptorLister, filesources []fileSource) ([]cfg.CommonCfg, error) {
	lgcInterceptor, err := interceptorLister.Get(interceptorRef)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// key: originSourceName, multi value: podName/containerName/originSourceName
	originSrcNameMap := make(map[string]sets.String)
	for _, fs := range filesources {
		podSrcName := fs.GetName()
		origin := getTypePodOriginSourceName(podSrcName)
		srcVal, ok := originSrcNameMap[origin]
		if !ok {
			originSrcNameMap[origin] = sets.NewString(podSrcName)
			continue
		}
		srcVal.Insert(podSrcName)
	}

	icpConfList := make([]index.ExtInterceptorConfig, 0)
	err = cfg.UnpackRaw([]byte(lgcInterceptor.Spec.Interceptors), &icpConfList)
	if err != nil {
		return nil, err
	}

	for i, extIcp := range icpConfList {
		if len(extIcp.BelongTo) == 0 {
			continue
		}

		newBelongTo := make([]string, 0)
		// update interceptor belongTo with podName/containerName/originSourceName
		for _, origin := range extIcp.BelongTo {
			podSrcNameSet, ok := originSrcNameMap[origin]
			if !ok {
				continue
			}
			newBelongTo = append(newBelongTo, podSrcNameSet.List()...)
		}

		icpConfList[i].BelongTo = newBelongTo
	}

	icpCommonCfg := make([]cfg.CommonCfg, 0)
	for _, v := range icpConfList {
		c, err := cfg.Pack(v)
		if err != nil {
			log.Info("pack interceptor config error: %+v", err)
			continue
		}
		icpCommonCfg = append(icpCommonCfg, c)
	}

	return icpCommonCfg, nil
}

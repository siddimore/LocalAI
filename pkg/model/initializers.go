package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/cpuid/v2"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/library"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"

	"github.com/elliotchance/orderedmap/v2"
)

var Aliases map[string]string = map[string]string{
	"go-llama":              LLamaCPP,
	"llama":                 LLamaCPP,
	"embedded-store":        LocalStoreBackend,
	"langchain-huggingface": LCHuggingFaceBackend,
}

var autoDetect = os.Getenv("DISABLE_AUTODETECT") != "true"

const (
	LlamaGGML = "llama-ggml"

	LLamaCPP = "llama-cpp"

	LLamaCPPAVX2     = "llama-cpp-avx2"
	LLamaCPPAVX      = "llama-cpp-avx"
	LLamaCPPFallback = "llama-cpp-fallback"
	LLamaCPPCUDA     = "llama-cpp-cuda"
	LLamaCPPHipblas  = "llama-cpp-hipblas"
	LLamaCPPSycl16   = "llama-cpp-sycl_16"
	LLamaCPPSycl32   = "llama-cpp-sycl_32"

	LLamaCPPGRPC = "llama-cpp-grpc"

	BertEmbeddingsBackend  = "bert-embeddings"
	RwkvBackend            = "rwkv"
	WhisperBackend         = "whisper"
	StableDiffusionBackend = "stablediffusion"
	TinyDreamBackend       = "tinydream"
	PiperBackend           = "piper"
	LCHuggingFaceBackend   = "huggingface"

	LocalStoreBackend = "local-store"
)

func backendPath(assetDir, backend string) string {
	return filepath.Join(assetDir, "backend-assets", "grpc", backend)
}

// backendsInAssetDir returns the list of backends in the asset directory
// that should be loaded
func backendsInAssetDir(assetDir string) ([]string, error) {
	// Exclude backends from automatic loading
	excludeBackends := []string{LocalStoreBackend}
	entry, err := os.ReadDir(backendPath(assetDir, ""))
	if err != nil {
		return nil, err
	}
	backends := make(map[string][]string)
ENTRY:
	for _, e := range entry {
		for _, exclude := range excludeBackends {
			if e.Name() == exclude {
				continue ENTRY
			}
		}
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".log") {
			continue
		}

		// Skip the llama.cpp variants if we are autoDetecting
		// But we always load the fallback variant if it exists
		if strings.Contains(e.Name(), LLamaCPP) && !strings.Contains(e.Name(), LLamaCPPFallback) && autoDetect {
			continue
		}

		backends[e.Name()] = []string{}
	}

	// if we are autoDetecting, we want to show the llama.cpp variants as a single backend
	if autoDetect {
		// if we find the llama.cpp variants, show them of as a single backend (llama-cpp) as later we are going to pick that up
		// when starting the service
		foundLCPPAVX, foundLCPPAVX2, foundLCPPFallback, foundLCPPGRPC, foundLCPPCuda, foundLCPPHipblas, foundSycl16, foundSycl32 := false, false, false, false, false, false, false, false
		if _, ok := backends[LLamaCPP]; !ok {
			for _, e := range entry {
				if strings.Contains(e.Name(), LLamaCPPAVX2) && !foundLCPPAVX2 {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPAVX2)
					foundLCPPAVX2 = true
				}
				if strings.Contains(e.Name(), LLamaCPPAVX) && !foundLCPPAVX {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPAVX)
					foundLCPPAVX = true
				}
				if strings.Contains(e.Name(), LLamaCPPFallback) && !foundLCPPFallback {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPFallback)
					foundLCPPFallback = true
				}
				if strings.Contains(e.Name(), LLamaCPPGRPC) && !foundLCPPGRPC {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPGRPC)
					foundLCPPGRPC = true
				}
				if strings.Contains(e.Name(), LLamaCPPCUDA) && !foundLCPPCuda {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPCUDA)
					foundLCPPCuda = true
				}
				if strings.Contains(e.Name(), LLamaCPPHipblas) && !foundLCPPHipblas {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPHipblas)
					foundLCPPHipblas = true
				}
				if strings.Contains(e.Name(), LLamaCPPSycl16) && !foundSycl16 {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPSycl16)
					foundSycl16 = true
				}
				if strings.Contains(e.Name(), LLamaCPPSycl32) && !foundSycl32 {
					backends[LLamaCPP] = append(backends[LLamaCPP], LLamaCPPSycl32)
					foundSycl32 = true
				}
			}
		}
	}

	// order backends from the asset directory.
	// as we scan for backends, we want to keep some order which backends are tried of.
	// for example, llama.cpp should be tried first, and we want to keep the huggingface backend at the last.

	// sets a priority list - first has more priority
	priorityList := []string{
		// First llama.cpp(variants) and llama-ggml to follow.
		// We keep the fallback to prevent that if the llama.cpp variants
		// that depends on shared libs if breaks have still a safety net.
		LLamaCPP, LlamaGGML, LLamaCPPFallback,
	}

	toTheEnd := []string{
		// last has to be huggingface
		LCHuggingFaceBackend,
		// then bert embeddings
		BertEmbeddingsBackend,
	}

	// create an ordered map
	orderedBackends := orderedmap.NewOrderedMap[string, any]()
	// add priorityList first
	for _, p := range priorityList {
		if _, ok := backends[p]; ok {
			orderedBackends.Set(p, backends[p])
		}
	}

	for k, v := range backends {
		if !slices.Contains(toTheEnd, k) {
			if _, ok := orderedBackends.Get(k); !ok {
				orderedBackends.Set(k, v)
			}
		}
	}

	for _, t := range toTheEnd {
		if _, ok := backends[t]; ok {
			orderedBackends.Set(t, backends[t])
		}
	}

	return orderedBackends.Keys(), nil
}

// selectGRPCProcess selects the GRPC process to start based on system capabilities
func selectGRPCProcess(backend, assetDir string, f16 bool) string {
	foundCUDA := false
	foundAMDGPU := false
	foundIntelGPU := false
	var grpcProcess string

	// Select backend now just for llama.cpp
	if backend != LLamaCPP {
		return ""
	}

	// Note: This environment variable is read by the LocalAI's llama.cpp grpc-server
	if os.Getenv("LLAMACPP_GRPC_SERVERS") != "" {
		log.Info().Msgf("[%s] attempting to load with GRPC variant", LLamaCPPGRPC)
		return backendPath(assetDir, LLamaCPPGRPC)
	}

	gpus, err := xsysinfo.GPUs()
	if err == nil {
		for _, gpu := range gpus {
			if strings.Contains(gpu.String(), "nvidia") {
				p := backendPath(assetDir, LLamaCPPCUDA)
				if _, err := os.Stat(p); err == nil {
					log.Info().Msgf("[%s] attempting to load with CUDA variant", backend)
					grpcProcess = p
					foundCUDA = true
				} else {
					log.Debug().Msgf("Nvidia GPU device found, no embedded CUDA variant found. You can ignore this message if you are using container with CUDA support")
				}
			}
			if strings.Contains(gpu.String(), "amd") {
				p := backendPath(assetDir, LLamaCPPHipblas)
				if _, err := os.Stat(p); err == nil {
					log.Info().Msgf("[%s] attempting to load with HIPBLAS variant", backend)
					grpcProcess = p
					foundAMDGPU = true
				} else {
					log.Debug().Msgf("AMD GPU device found, no embedded HIPBLAS variant found. You can ignore this message if you are using container with HIPBLAS support")
				}
			}
			if strings.Contains(gpu.String(), "intel") {
				backend := LLamaCPPSycl16
				if !f16 {
					backend = LLamaCPPSycl32
				}
				p := backendPath(assetDir, backend)
				if _, err := os.Stat(p); err == nil {
					log.Info().Msgf("[%s] attempting to load with Intel variant", backend)
					grpcProcess = p
					foundIntelGPU = true
				} else {
					log.Debug().Msgf("Intel GPU device found, no embedded SYCL variant found. You can ignore this message if you are using container with SYCL support")
				}
			}
		}
	}

	if foundCUDA || foundAMDGPU || foundIntelGPU {
		return grpcProcess
	}

	if xsysinfo.HasCPUCaps(cpuid.AVX2) {
		p := backendPath(assetDir, LLamaCPPAVX2)
		if _, err := os.Stat(p); err == nil {
			log.Info().Msgf("[%s] attempting to load with AVX2 variant", backend)
			grpcProcess = p
		}
	} else if xsysinfo.HasCPUCaps(cpuid.AVX) {
		p := backendPath(assetDir, LLamaCPPAVX)
		if _, err := os.Stat(p); err == nil {
			log.Info().Msgf("[%s] attempting to load with AVX variant", backend)
			grpcProcess = p
		}
	} else {
		p := backendPath(assetDir, LLamaCPPFallback)
		if _, err := os.Stat(p); err == nil {
			log.Info().Msgf("[%s] attempting to load with fallback variant", backend)
			grpcProcess = p
		}
	}

	return grpcProcess
}

// starts the grpcModelProcess for the backend, and returns a grpc client
// It also loads the model
func (ml *ModelLoader) grpcModel(backend string, o *Options) func(string, string) (*Model, error) {
	return func(modelName, modelFile string) (*Model, error) {

		log.Debug().Msgf("Loading Model %s with gRPC (file: %s) (backend: %s): %+v", modelName, modelFile, backend, *o)

		var client *Model

		getFreeAddress := func() (string, error) {
			port, err := freeport.GetFreePort()
			if err != nil {
				return "", fmt.Errorf("failed allocating free ports: %s", err.Error())
			}
			return fmt.Sprintf("127.0.0.1:%d", port), nil
		}

		// If no specific model path is set for transformers/HF, set it to the model path
		for _, env := range []string{"HF_HOME", "TRANSFORMERS_CACHE", "HUGGINGFACE_HUB_CACHE"} {
			if os.Getenv(env) == "" {
				err := os.Setenv(env, ml.ModelPath)
				if err != nil {
					log.Error().Err(err).Str("name", env).Str("modelPath", ml.ModelPath).Msg("unable to set environment variable to modelPath")
				}
			}
		}

		// Check if the backend is provided as external
		if uri, ok := o.externalBackends[backend]; ok {
			log.Debug().Msgf("Loading external backend: %s", uri)
			// check if uri is a file or a address
			if fi, err := os.Stat(uri); err == nil {
				log.Debug().Msgf("external backend is file: %+v", fi)
				serverAddress, err := getFreeAddress()
				if err != nil {
					return nil, fmt.Errorf("failed allocating free ports: %s", err.Error())
				}
				// Make sure the process is executable
				process, err := ml.startProcess(uri, o.model, serverAddress)
				if err != nil {
					log.Error().Err(err).Str("path", uri).Msg("failed to launch ")
					return nil, err
				}

				log.Debug().Msgf("GRPC Service Started")

				client = NewModel(modelName, serverAddress, process)
			} else {
				log.Debug().Msg("external backend is uri")
				// address
				client = NewModel(modelName, uri, nil)
			}
		} else {
			grpcProcess := backendPath(o.assetDir, backend)
			if err := utils.VerifyPath(grpcProcess, o.assetDir); err != nil {
				return nil, fmt.Errorf("refering to a backend not in asset dir: %s", err.Error())
			}

			if autoDetect {
				// autoDetect GRPC process to start based on system capabilities
				if selectedProcess := selectGRPCProcess(backend, o.assetDir, o.gRPCOptions.F16Memory); selectedProcess != "" {
					grpcProcess = selectedProcess
				}
			}

			// Check if the file exists
			if _, err := os.Stat(grpcProcess); os.IsNotExist(err) {
				return nil, fmt.Errorf("backend not found: %s", grpcProcess)
			}

			serverAddress, err := getFreeAddress()
			if err != nil {
				return nil, fmt.Errorf("failed allocating free ports: %s", err.Error())
			}

			args := []string{}

			// Load the ld.so if it exists
			args, grpcProcess = library.LoadLDSO(o.assetDir, args, grpcProcess)

			// Make sure the process is executable in any circumstance
			process, err := ml.startProcess(grpcProcess, o.model, serverAddress, args...)
			if err != nil {
				return nil, err
			}

			log.Debug().Msgf("GRPC Service Started")

			client = NewModel(modelName, serverAddress, process)
		}

		log.Debug().Msgf("Wait for the service to start up")

		// Wait for the service to start up
		ready := false
		for i := 0; i < o.grpcAttempts; i++ {
			alive, err := client.GRPC(o.parallelRequests, ml.wd).HealthCheck(context.Background())
			if alive {
				log.Debug().Msgf("GRPC Service Ready")
				ready = true
				break
			}
			if err != nil && i == o.grpcAttempts-1 {
				log.Error().Err(err).Msg("failed starting/connecting to the gRPC service")
			}
			time.Sleep(time.Duration(o.grpcAttemptsDelay) * time.Second)
		}

		if !ready {
			log.Debug().Msgf("GRPC Service NOT ready")
			ml.deleteProcess(o.model)
			return nil, fmt.Errorf("grpc service not ready")
		}

		options := *o.gRPCOptions
		options.Model = modelName
		options.ModelFile = modelFile

		log.Debug().Msgf("GRPC: Loading model with options: %+v", options)

		res, err := client.GRPC(o.parallelRequests, ml.wd).LoadModel(o.context, &options)
		if err != nil {
			ml.deleteProcess(o.model)
			return nil, fmt.Errorf("could not load model: %w", err)
		}
		if !res.Success {
			ml.deleteProcess(o.model)
			return nil, fmt.Errorf("could not load model (no success): %s", res.Message)
		}

		return client, nil
	}
}

func (ml *ModelLoader) ListAvailableBackends(assetdir string) ([]string, error) {
	return backendsInAssetDir(assetdir)
}

func (ml *ModelLoader) BackendLoader(opts ...Option) (client grpc.Backend, err error) {
	o := NewOptions(opts...)

	if o.model != "" {
		log.Info().Msgf("Loading model '%s' with backend %s", o.model, o.backendString)
	} else {
		log.Info().Msgf("Loading model with backend %s", o.backendString)
	}

	backend := strings.ToLower(o.backendString)
	if realBackend, exists := Aliases[backend]; exists {
		backend = realBackend
		log.Debug().Msgf("%s is an alias of %s", backend, realBackend)
	}

	if o.singleActiveBackend {
		log.Debug().Msgf("Stopping all backends except '%s'", o.model)
		err := ml.StopGRPC(allExcept(o.model))
		if err != nil {
			log.Error().Err(err).Str("keptModel", o.model).Msg("error while shutting down all backends except for the keptModel")
		}
	}

	var backendToConsume string

	switch backend {
	case PiperBackend:
		o.gRPCOptions.LibrarySearchPath = filepath.Join(o.assetDir, "backend-assets", "espeak-ng-data")
		backendToConsume = PiperBackend
	default:
		backendToConsume = backend
	}

	model, err := ml.LoadModel(o.model, ml.grpcModel(backendToConsume, o))
	if err != nil {
		return nil, err
	}

	return model.GRPC(o.parallelRequests, ml.wd), nil
}

func (ml *ModelLoader) GreedyLoader(opts ...Option) (grpc.Backend, error) {
	o := NewOptions(opts...)

	// Return earlier if we have a model already loaded
	// (avoid looping through all the backends)
	if m := ml.CheckIsLoaded(o.model); m != nil {
		log.Debug().Msgf("Model '%s' already loaded", o.model)

		return m.GRPC(o.parallelRequests, ml.wd), nil
	}

	// If we can have only one backend active, kill all the others (except external backends)
	if o.singleActiveBackend {
		log.Debug().Msgf("Stopping all backends except '%s'", o.model)
		err := ml.StopGRPC(allExcept(o.model))
		if err != nil {
			log.Error().Err(err).Str("keptModel", o.model).Msg("error while shutting down all backends except for the keptModel - greedyloader continuing")
		}
	}

	var err error

	// get backends embedded in the binary
	autoLoadBackends, err := backendsInAssetDir(o.assetDir)
	if err != nil {
		return nil, err
	}

	// append externalBackends supplied by the user via the CLI
	for _, b := range o.externalBackends {
		autoLoadBackends = append(autoLoadBackends, b)
	}

	log.Debug().Msgf("Loading from the following backends (in order): %+v", autoLoadBackends)

	if o.model != "" {
		log.Info().Msgf("Trying to load the model '%s' with the backend '%s'", o.model, autoLoadBackends)
	}

	for _, key := range autoLoadBackends {
		log.Info().Msgf("[%s] Attempting to load", key)
		options := []Option{
			WithBackendString(key),
			WithModel(o.model),
			WithLoadGRPCLoadModelOpts(o.gRPCOptions),
			WithThreads(o.threads),
			WithAssetDir(o.assetDir),
		}

		for k, v := range o.externalBackends {
			options = append(options, WithExternalBackend(k, v))
		}

		model, modelerr := ml.BackendLoader(options...)
		if modelerr == nil && model != nil {
			log.Info().Msgf("[%s] Loads OK", key)
			return model, nil
		} else if modelerr != nil {
			err = errors.Join(err, fmt.Errorf("[%s]: %w", key, modelerr))
			log.Info().Msgf("[%s] Fails: %s", key, modelerr.Error())
		} else if model == nil {
			err = errors.Join(err, fmt.Errorf("backend %s returned no usable model", key))
			log.Info().Msgf("[%s] Fails: %s", key, "backend returned no usable model")
		}

		if autoDetect && key == LLamaCPP && err != nil {
			// try as hard as possible to run the llama.cpp variants
			backendToUse := ""
			if xsysinfo.HasCPUCaps(cpuid.AVX2) {
				if _, err := os.Stat(backendPath(o.assetDir, LLamaCPPAVX2)); err == nil {
					backendToUse = LLamaCPPAVX2
				}
			} else if xsysinfo.HasCPUCaps(cpuid.AVX) {
				if _, err := os.Stat(backendPath(o.assetDir, LLamaCPPAVX2)); err == nil {
					backendToUse = LLamaCPPAVX
				}
			} else {
				if _, err := os.Stat(backendPath(o.assetDir, LLamaCPPFallback)); err == nil {
					backendToUse = LLamaCPPFallback
				} else {
					// If we don't have a fallback, just skip fallback
					continue
				}
			}

			// Autodetection failed, try the fallback
			log.Info().Msgf("[%s] Autodetection failed, trying the fallback", key)
			options = append(options, WithBackendString(backendToUse))
			model, modelerr = ml.BackendLoader(options...)
			if modelerr == nil && model != nil {
				log.Info().Msgf("[%s] Loads OK", key)
				return model, nil
			} else {
				err = errors.Join(err, fmt.Errorf("[%s]: %w", key, modelerr))
				log.Info().Msgf("[%s] Fails: %s", key, modelerr.Error())
			}
		}
	}

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}

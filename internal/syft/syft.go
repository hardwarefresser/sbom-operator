package syft

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/anchore/syft/syft"
	"github.com/anchore/syft/syft/format"
	"github.com/anchore/syft/syft/pkg/cataloger"
	"github.com/anchore/syft/syft/sbom"

	"github.com/anchore/syft/syft/source"
	util "github.com/ckotzbauer/sbom-operator/internal"
	"github.com/ckotzbauer/sbom-operator/internal/kubernetes"
	"github.com/ckotzbauer/sbom-operator/internal/registry"
	"github.com/sirupsen/logrus"
)

type Syft struct {
	GitWorkingTree string
	GitPath        string
	SbomFormat     string
}

func New(gitWorkingTree, gitPath, sbomFormat string) Syft {
	return Syft{
		GitWorkingTree: gitWorkingTree,
		GitPath:        gitPath,
		SbomFormat:     sbomFormat,
	}
}

func (s *Syft) ExecuteSyft(img kubernetes.ImageDigest) (string, error) {
	fileName := GetFileName(s.SbomFormat)
	filePath := strings.ReplaceAll(img.Digest, "@", "/")
	filePath = strings.ReplaceAll(path.Join(s.GitWorkingTree, s.GitPath, filePath, fileName), ":", "_")

	logrus.Debugf("Processing image %s", img.Digest)

	workDir := "/tmp/" + util.RandStringBytes(10)
	imagePath := workDir + "/image.tar.gz"
	os.Mkdir(workDir, 0777)

	err := registry.SaveImage(imagePath, workDir, img)

	if err != nil {
		logrus.WithError(err).Error("Image-Pull failed")
		return "", err
	}

	src, cleanup, err := source.New(filepath.Join("file:", imagePath), nil, nil)
	if err != nil {
		logrus.WithError(fmt.Errorf("failed to construct source from input %s: %w", imagePath, err)).Error("Source-Creation failed")
		return "", err
	}

	if cleanup != nil {
		defer cleanup()
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		logrus.Warnf("failed to read build info")
	}

	descriptor := sbom.Descriptor{
		Name: "syft",
	}

	for _, dep := range bi.Deps {
		if strings.EqualFold("github.com/anchore/syft", dep.Path) {
			descriptor.Version = dep.Version
		}
	}

	result := sbom.SBOM{
		Source:     src.Metadata,
		Descriptor: descriptor,
		// TODO: we should have helper functions for getting this built from exported library functions
	}

	c := cataloger.DefaultConfig()
	c.Search.Scope = source.SquashedScope
	packageCatalog, relationships, theDistro, err := syft.CatalogPackages(src, c)
	if err != nil {
		logrus.WithError(err).Error("CatalogPackages failed")
		return "", err
	}

	result.Artifacts.PackageCatalog = packageCatalog
	result.Artifacts.LinuxDistribution = theDistro
	result.Relationships = relationships

	// you can use other formats such as format.CycloneDxJSONOption or format.SPDXJSONOption ...
	b, err := syft.Encode(result, format.Option(s.SbomFormat))
	if err != nil {
		logrus.WithError(err).Error("Encoding of result failed")
		return "", err
	}

	os.RemoveAll(workDir)

	dir := filepath.Dir(filePath)
	err = os.MkdirAll(dir, 0777)
	if err != nil {
		logrus.WithError(err).Error("Directory could not be created")
		return "", err
	}

	err = os.WriteFile(filePath, b, 0640)
	if err != nil {
		logrus.WithError(err).Error("SBOM could not be saved")
		return "", err
	}

	return filePath, nil
}

func GetFileName(sbomFormat string) string {
	switch sbomFormat {
	case "json":
		return "sbom.json"
	case "text":
		return "sbom.txt"
	case "cyclonedx":
		return "sbom.xml"
	case "cyclonedx-json":
		return "sbom.json"
	case "spdx":
		return "sbom.spdx"
	case "spdx-json":
		return "sbom.json"
	case "table":
		return "sbom.txt"
	default:
		return "sbom.json"
	}
}

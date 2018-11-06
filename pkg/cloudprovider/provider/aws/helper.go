package aws

import (
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/golang/glog"
	gocache "github.com/patrickmn/go-cache"

	"github.com/kubermatic/machine-controller/pkg/providerconfig"

	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	volumeTypes = sets.NewString(
		ec2.VolumeTypeStandard,
		ec2.VolumeTypeIo1,
		ec2.VolumeTypeGp2,
		ec2.VolumeTypeSc1,
		ec2.VolumeTypeSt1,
	)

	amiFilters = map[providerconfig.OperatingSystem]amiFilter{
		providerconfig.OperatingSystemCoreos: {
			description: "CoreOS Container Linux stable*",
			// The AWS marketplace ID from CoreOS
			owner: "595879546273",
		},
		providerconfig.OperatingSystemCentOS: {
			description: "CentOS Linux 7 x86_64 HVM EBS*",
			// The AWS marketplace ID from AWS
			owner: "679593333241",
		},
		providerconfig.OperatingSystemUbuntu: {
			// Be as precise as possible - otherwise we might get a nightly dev build
			description: "Canonical, Ubuntu, 18.04 LTS, amd64 bionic image build on ????-??-??",
			// The AWS marketplace ID from Canonical
			owner: "099720109477",
		},
	}

	// cacheLock protects concurrent cache misses against a single key. This usually happens when multiple machines get created simultaneously
	// We lock so the first access updates/writes the data to the cache and afterwards everyone reads the cached data
	cacheLock = &sync.Mutex{}
	cache     = gocache.New(5*time.Minute, 5*time.Minute)
)

func getSession(id, secret, token, region string) (*session.Session, error) {
	config := aws.NewConfig()
	config = config.WithRegion(region)
	config = config.WithCredentials(credentials.NewStaticCredentials(id, secret, token))
	config = config.WithMaxRetries(maxRetries)
	return session.NewSession(config)
}

func getIAMclient(id, secret, region string) (*iam.IAM, error) {
	sess, err := getSession(id, secret, "", region)
	if err != nil {
		return nil, awsErrorToTerminalError(err, "failed to get aws session")
	}
	return iam.New(sess), nil
}

func getEC2client(id, secret, region string) (*ec2.EC2, error) {
	sess, err := getSession(id, secret, "", region)
	if err != nil {
		return nil, awsErrorToTerminalError(err, "failed to get aws session")
	}
	return ec2.New(sess), nil
}

func getDefaultRootDevicePath(os providerconfig.OperatingSystem) (string, error) {
	switch os {
	case providerconfig.OperatingSystemUbuntu:
		return "/dev/sda1", nil
	case providerconfig.OperatingSystemCentOS:
		return "/dev/sda1", nil
	case providerconfig.OperatingSystemCoreos:
		return "/dev/xvda", nil
	}

	return "", fmt.Errorf("no default root path found for %s operating system", os)
}

func getVpc(client *ec2.EC2, id string) (*ec2.Vpc, error) {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	cacheKey := fmt.Sprintf("vpc-%s-%s", *client.Config.Region, id)
	if vpc, found := cache.Get(cacheKey); found {
		glog.V(6).Infof("Found VPC %s in cache", *vpc.(*ec2.Vpc).VpcId)
		return vpc.(*ec2.Vpc), nil
	}

	vpcOut, err := client.DescribeVpcs(&ec2.DescribeVpcsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("vpc-id"), Values: []*string{aws.String(id)}},
		},
	})

	if err != nil {
		return nil, awsErrorToTerminalError(err, "failed to list vpc's")
	}

	if len(vpcOut.Vpcs) != 1 {
		return nil, fmt.Errorf("unable to find specified vpc with id %q", id)
	}

	cache.SetDefault(cacheKey, vpcOut.Vpcs[0])
	return vpcOut.Vpcs[0], nil
}

type amiFilter struct {
	description string
	owner       string
}

func getDefaultAMIID(client *ec2.EC2, os providerconfig.OperatingSystem) (string, error) {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	filter, osSupported := amiFilters[os]
	if !osSupported {
		return "", fmt.Errorf("operating system %q not supported", os)
	}

	cacheKey := fmt.Sprintf("ami-id-%s-%s", *client.Config.Region, os)
	if amiID, found := cache.Get(cacheKey); found {
		glog.V(6).Infof("Found AMI ID %s in cache", amiID.(string))
		return amiID.(string), nil
	}

	imagesOut, err := client.DescribeImages(&ec2.DescribeImagesInput{
		Owners: aws.StringSlice([]string{filter.owner}),
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("description"),
				Values: aws.StringSlice([]string{filter.description}),
			},
			{
				Name:   aws.String("virtualization-type"),
				Values: aws.StringSlice([]string{"hvm"}),
			},
			{
				Name:   aws.String("root-device-type"),
				Values: aws.StringSlice([]string{"ebs"}),
			},
		},
	})
	if err != nil {
		return "", err
	}

	if len(imagesOut.Images) == 0 {
		return "", fmt.Errorf("could not find Image for '%s'", os)
	}

	image := imagesOut.Images[0]
	for _, v := range imagesOut.Images {
		itime, _ := time.Parse(time.RFC3339, *image.CreationDate)
		vtime, _ := time.Parse(time.RFC3339, *v.CreationDate)
		if vtime.After(itime) {
			image = v
		}
	}

	cache.SetDefault(cacheKey, *image.ImageId)
	return *image.ImageId, nil
}

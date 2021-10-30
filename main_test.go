package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersionCompare(t *testing.T) {
	testCases := []struct {
		versionRequirement string
		version            string
		expectedResult     bool
	}{
		{
			versionRequirement: ">=1.9.0",
			version:            "1.10.0",
			expectedResult:     true,
		},
		{
			versionRequirement: ">=1.3.0",
			version:            "1.3.0",
			expectedResult:     true,
		},
		{
			versionRequirement: "  >=1.3.0",
			version:            "1.2.0",
			expectedResult:     false,
		},
		{
			versionRequirement: ">v1.3.0   ",
			version:            "v1.4.0",
			expectedResult:     true,
		},
		{
			versionRequirement: ">1.3.0",
			version:            " v1.3.0",
			expectedResult:     false,
		},
		{
			versionRequirement: ">v1.3.0",
			version:            " 1.2.0",
			expectedResult:     false,
		},
		{
			versionRequirement: "1.3.0 ",
			version:            "1.2.0",
			expectedResult:     false,
		},
		{
			versionRequirement: " 1.3.0",
			version:            "1.3.0",
			expectedResult:     true,
		},
		{
			versionRequirement: "",
			version:            "1.3.0",
			expectedResult:     true,
		},
	}
	for _, tCase := range testCases {
		assert.Equal(t, tCase.expectedResult, versionRequired(tCase.versionRequirement, tCase.version), "version check result not match")
	}
}

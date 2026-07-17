package metrics

import "strconv"

const (
	tagKeyApiName      tagKey = "api_name"
	tagKeyResponseCode tagKey = "response_code"
)

func TagApiNameFromProtoFullMethod(fullMethod string) Tag {
	return Tag{
		Key:   tagKeyApiName,
		Value: tagValue(fullMethod),
	}
}

func TagApiName(apiName string) Tag {
	return Tag{
		Key:   tagKeyApiName,
		Value: tagValue(apiName),
	}
}

func TagResponseCode(code int) Tag {
	return Tag{
		Key:   tagKeyResponseCode,
		Value: tagValue(strconv.Itoa(code)),
	}
}

func TagResponseCodeString(code string) Tag {
	return Tag{
		Key:   tagKeyResponseCode,
		Value: tagValue(code),
	}
}

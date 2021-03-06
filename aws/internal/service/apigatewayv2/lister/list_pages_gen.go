// Code generated by "aws/internal/generators/listpages/main.go -function=GetApis,GetDomainNames github.com/aws/aws-sdk-go/service/apigatewayv2"; DO NOT EDIT.

package lister

import (
	"context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/apigatewayv2"
)

func GetApisPages(conn *apigatewayv2.ApiGatewayV2, input *apigatewayv2.GetApisInput, fn func(*apigatewayv2.GetApisOutput, bool) bool) error {
	return GetApisPagesWithContext(context.Background(), conn, input, fn)
}

func GetApisPagesWithContext(ctx context.Context, conn *apigatewayv2.ApiGatewayV2, input *apigatewayv2.GetApisInput, fn func(*apigatewayv2.GetApisOutput, bool) bool) error {
	for {
		output, err := conn.GetApisWithContext(ctx, input)
		if err != nil {
			return err
		}

		lastPage := aws.StringValue(output.NextToken) == ""
		if !fn(output, lastPage) || lastPage {
			break
		}

		input.NextToken = output.NextToken
	}
	return nil
}

func GetDomainNamesPages(conn *apigatewayv2.ApiGatewayV2, input *apigatewayv2.GetDomainNamesInput, fn func(*apigatewayv2.GetDomainNamesOutput, bool) bool) error {
	return GetDomainNamesPagesWithContext(context.Background(), conn, input, fn)
}

func GetDomainNamesPagesWithContext(ctx context.Context, conn *apigatewayv2.ApiGatewayV2, input *apigatewayv2.GetDomainNamesInput, fn func(*apigatewayv2.GetDomainNamesOutput, bool) bool) error {
	for {
		output, err := conn.GetDomainNamesWithContext(ctx, input)
		if err != nil {
			return err
		}

		lastPage := aws.StringValue(output.NextToken) == ""
		if !fn(output, lastPage) || lastPage {
			break
		}

		input.NextToken = output.NextToken
	}
	return nil
}

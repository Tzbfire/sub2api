package openaiimages

import (
	"context"
	"errors"
	"sync"
)

type multiImageDispatchResult struct {
	res *DispatchResult
	err error
}

// DispatchMultiImage splits a multi-image request into N single-image dispatches.
// Each single dispatch goes through normal account selection and retry handling,
// allowing ImagePool to spread work across multiple available accounts.
func DispatchMultiImage(
	ctx context.Context,
	src AccountSource,
	drivers DriverRegistry,
	in DispatchInput,
	opts DispatchOptions,
	parallelism int,
) (*DispatchResult, error) {
	if in.Request == nil || in.Request.N <= 1 {
		return Dispatch(ctx, src, drivers, in, opts)
	}

	n := in.Request.N
	if parallelism <= 0 || parallelism > n {
		parallelism = n
	}

	results := make([]multiImageDispatchResult, n)
	pending := make([]int, n)
	for i := range pending {
		pending[i] = i
	}

	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			for _, idx := range pending {
				results[idx] = multiImageDispatchResult{err: err}
			}
			break
		}

		waveSize := parallelism
		if waveSize > len(pending) {
			waveSize = len(pending)
		}
		wave := append([]int(nil), pending[:waveSize]...)
		pending = pending[waveSize:]

		waveResults := dispatchMultiImageWave(ctx, src, drivers, in, opts, wave)
		waveSuccesses := 0
		var retryNoAccount []int
		for _, idx := range wave {
			result := waveResults[idx]
			if result.err == nil && result.res != nil && result.res.Result != nil && len(result.res.Result.Items) > 0 {
				waveSuccesses++
				results[idx] = result
				continue
			}
			if errors.Is(result.err, ErrNoAccountAvailable) {
				retryNoAccount = append(retryNoAccount, idx)
				continue
			}
			results[idx] = result
		}

		if len(retryNoAccount) == 0 {
			continue
		}
		if waveSuccesses == 0 {
			for _, idx := range retryNoAccount {
				results[idx] = waveResults[idx]
			}
			continue
		}
		pending = append(retryNoAccount, pending...)
	}

	return mergeMultiImageResults(results)
}

func dispatchMultiImageWave(
	ctx context.Context,
	src AccountSource,
	drivers DriverRegistry,
	in DispatchInput,
	opts DispatchOptions,
	wave []int,
) map[int]multiImageDispatchResult {
	results := make(map[int]multiImageDispatchResult, len(wave))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, idx := range wave {
		wg.Add(1)
		go func(imageIndex int) {
			defer wg.Done()
			res, err := dispatchSingleImage(ctx, src, drivers, in, opts)
			mu.Lock()
			results[imageIndex] = multiImageDispatchResult{res: res, err: err}
			mu.Unlock()
		}(idx)
	}
	wg.Wait()
	return results
}

func dispatchSingleImage(
	ctx context.Context,
	src AccountSource,
	drivers DriverRegistry,
	in DispatchInput,
	opts DispatchOptions,
) (*DispatchResult, error) {
	if in.Request == nil {
		return Dispatch(ctx, src, drivers, in, opts)
	}
	singleReq := *in.Request
	singleReq.N = 1
	singleIn := in
	singleIn.Request = &singleReq
	return Dispatch(ctx, src, drivers, singleIn, opts)
}

func mergeMultiImageResults(results []multiImageDispatchResult) (*DispatchResult, error) {
	var (
		firstErr     error
		firstSuccess *DispatchResult
		merged       ImageResult
		attempts     int
		driverUsed   string
	)

	for _, item := range results {
		if item.err != nil {
			if firstErr == nil {
				firstErr = item.err
			}
			continue
		}
		if item.res == nil || item.res.Result == nil || len(item.res.Result.Items) == 0 {
			continue
		}
		if firstSuccess == nil {
			firstSuccess = item.res
			merged.Model = item.res.Result.Model
			merged.Created = item.res.Result.Created
			driverUsed = item.res.DriverUsed
		} else if driverUsed != item.res.DriverUsed {
			driverUsed = "mixed"
		}

		attempts += item.res.Attempts
		merged.Items = append(merged.Items, item.res.Result.Items...)
		merged.Usage.InputTokens += item.res.Result.Usage.InputTokens
		merged.Usage.OutputTokens += item.res.Result.Usage.OutputTokens
		merged.Usage.TotalTokens += item.res.Result.Usage.TotalTokens
	}
	merged.Usage.ImagesCount = len(merged.Items)

	if len(merged.Items) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, ErrMaxAttemptsExceeded
	}
	if attempts == 0 && firstSuccess != nil {
		attempts = firstSuccess.Attempts
	}

	return &DispatchResult{
		Result:     &merged,
		Account:    firstSuccess.Account,
		DriverUsed: driverUsed,
		Attempts:   attempts,
	}, nil
}

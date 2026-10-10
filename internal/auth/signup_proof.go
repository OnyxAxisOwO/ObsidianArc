package auth

import (
	"context"
	"errors"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
)

// verifyProof checks the proof-of-work solution a sign-up arrived with. A form
// sign-up checks it in Register; a provider sign-up checks it in
// CheckSignupProof. Both refuse the same way and log under the same event, so
// the security log reads the same whichever door the sign-up came through.
func (s *Service) verifyProof(ctx context.Context, ip, username string, solution *pow.Solution) error {
	if s.PoW == nil {
		return errors.New("auth: pow required but manager not configured")
	}
	if solution == nil {
		if s.OnChallengeFailure != nil {
			s.OnChallengeFailure(ctx, "pow_challenge", ip, username, "缺少 PoW 解答")
		}
		return pow.ErrMissingSolution
	}
	if err := s.PoW.Verify(solution); err != nil {
		reason := "PoW 校验失败"
		switch {
		case errors.Is(err, pow.ErrExpired):
			reason = "PoW 挑战已过期"
		case errors.Is(err, pow.ErrInvalidSignature):
			reason = "PoW 签名无效"
		case errors.Is(err, pow.ErrMaxExceeded):
			reason = "PoW 步数超出上限"
		case errors.Is(err, pow.ErrInvalidNonce):
			reason = "PoW 计算结果不匹配"
		case errors.Is(err, pow.ErrReplayed):
			reason = "PoW 挑战已被使用"
		}
		if s.OnChallengeFailure != nil {
			s.OnChallengeFailure(ctx, "pow_challenge", ip, username, reason)
		}
		return err
	}
	return nil
}

// CheckSignupProof is the proof-of-work half of the sign-up challenge for a
// provider sign-up. The solution comes in on the start request, which is the
// browser's own navigation to this server: the provider's redirect back carries
// nothing the browser solved, so the callback has no solution to check. The
// solution is spent here, as a form's is, and the attempt counts toward the
// difficulty the next challenge from this address is given.
func (s *Service) CheckSignupProof(ctx context.Context, ip string, solution *pow.Solution) error {
	if err := s.verifyProof(ctx, ip, "", solution); err != nil {
		return err
	}
	if tracker := s.PoW.Tracker(); tracker != nil {
		tracker.RecordAttempt(ip, time.Now())
	}
	return nil
}

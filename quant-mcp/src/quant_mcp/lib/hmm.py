"""手写高斯 HMM（Baum-Welch + Viterbi，纯 numpy）——第12课 Regime 检测。

调试教训（来自 quant-study 05）：
  观测似然必须按【时刻】(行) 缩放，保留状态间相对大小；
  按状态内缩放会抹掉相对信息导致 EM 塌缩到单状态。
"""

import numpy as np


class GaussianHMM:
    """高斯 HMM：观测为多元高斯，协方差为对角阵。手写 EM。"""

    def __init__(self, n_states: int, n_iter: int = 200, tol: float = 1e-4,
                 seed: int = 0):
        self.n_states = n_states
        self.n_iter = n_iter
        self.tol = tol
        self.rng = np.random.default_rng(seed)

    def _init_params(self, X: np.ndarray):
        T, D = X.shape
        order = np.argsort(X.mean(axis=1))
        bounds = np.linspace(0, T, self.n_states + 1).astype(int)
        self.mu = np.stack([X[order[bounds[k]:bounds[k + 1]]].mean(axis=0)
                            for k in range(self.n_states)])
        self.sigma2 = np.stack([np.clip(X[order[bounds[k]:bounds[k + 1]]].var(axis=0),
                                        self.var_floor, None)
                                for k in range(self.n_states)])
        self.pi = np.full(self.n_states, 1.0 / self.n_states)
        self.A = np.full((self.n_states, self.n_states), 1.0 / self.n_states)
        vol = self.sigma2.sum(axis=1)
        perm = np.argsort(vol)
        self.mu, self.sigma2 = self.mu[perm], self.sigma2[perm]
        self.pi = self.pi[perm]
        self.A = self.A[np.ix_(perm, perm)]

    def _obs_lik(self, X: np.ndarray) -> np.ndarray:
        """(T, K) 观测似然：按【时刻】缩放，保留状态间相对大小。"""
        T = X.shape[0]
        loglik = np.zeros((T, self.n_states))
        for k in range(self.n_states):
            diff = X - self.mu[k]
            var = self.sigma2[k]
            loglik[:, k] = -0.5 * np.sum(diff ** 2 / var + np.log(2 * np.pi * var), axis=1)
        return np.exp(loglik - loglik.max(axis=1, keepdims=True))

    def _forward_backward(self, X: np.ndarray):
        T, K = X.shape[0], self.n_states
        B = self._obs_lik(X)
        alpha = np.zeros((T, K))
        scale = np.zeros(T)
        alpha[0] = self.pi * B[0]
        scale[0] = alpha[0].sum() + 1e-300
        alpha[0] /= scale[0]
        for t in range(1, T):
            alpha[t] = (alpha[t - 1] @ self.A) * B[t]
            scale[t] = alpha[t].sum() + 1e-300
            alpha[t] /= scale[t]
        beta = np.zeros((T, K))
        beta[-1] = 1.0
        for t in range(T - 2, -1, -1):
            beta[t] = (self.A @ (B[t + 1] * beta[t + 1])) / scale[t + 1]
        gamma = alpha * beta
        gamma /= gamma.sum(axis=1, keepdims=True) + 1e-300
        xi = np.zeros((T - 1, K, K))
        for t in range(T - 1):
            m = alpha[t][:, None] * self.A * (B[t + 1] * beta[t + 1])[None, :]
            xi[t] = m / (m.sum() + 1e-300)
        return gamma, xi, scale

    def fit(self, X: np.ndarray) -> "GaussianHMM":
        X = np.asarray(X, dtype=float)
        self._feat_mean = X.mean(axis=0)
        self._feat_std = X.std(axis=0) + 1e-12
        Xs = (X - self._feat_mean) / self._feat_std
        self.var_floor = 0.01
        self._init_params(Xs)
        prev_loglik = -np.inf
        for _ in range(self.n_iter):
            gamma, xi, scale = self._forward_backward(Xs)
            self.pi = gamma[0]
            self.A = xi.sum(axis=0)
            self.A /= self.A.sum(axis=1, keepdims=True) + 1e-300
            gsum = gamma.sum(axis=0)
            self.mu = (gamma.T @ Xs) / gsum[:, None]
            for k in range(self.n_states):
                diff = Xs - self.mu[k]
                self.sigma2[k] = (gamma[:, k][:, None] * diff ** 2).sum(axis=0) / gsum[k]
                self.sigma2[k] = np.clip(self.sigma2[k], self.var_floor, None)
            loglik = np.sum(np.log(scale))
            if loglik < prev_loglik:
                break
            if loglik - prev_loglik < self.tol:
                break
            prev_loglik = loglik
        self.mu = self.mu * self._feat_std + self._feat_mean
        self.sigma2 = self.sigma2 * self._feat_std ** 2
        return self

    def predict_proba(self, X: np.ndarray) -> np.ndarray:
        gamma, _, _ = self._forward_backward(np.asarray(X, dtype=float))
        return gamma

    def decode(self, X: np.ndarray) -> np.ndarray:
        """Viterbi 解码，返回最优状态序列 (T,)。"""
        X = np.asarray(X, dtype=float)
        T, K = X.shape[0], self.n_states
        B = self._obs_lik(X)
        logB = np.log(B + 1e-300)
        logA = np.log(self.A + 1e-300)
        logpi = np.log(self.pi + 1e-300)
        delta = np.zeros((T, K))
        psi = np.zeros((T, K), dtype=int)
        delta[0] = logpi + logB[0]
        for t in range(1, T):
            for k in range(K):
                tmp = delta[t - 1] + logA[:, k]
                psi[t, k] = np.argmax(tmp)
                delta[t, k] = tmp[psi[t, k]] + logB[t, k]
        path = np.zeros(T, dtype=int)
        path[-1] = np.argmax(delta[-1])
        for t in range(T - 2, -1, -1):
            path[t] = psi[t + 1, path[t + 1]]
        return path


def build_features(rets: np.ndarray, win: int = 20) -> np.ndarray:
    """特征：日收益率 + 滚动波动率。"""
    vol = np.zeros_like(rets)
    for i in range(win, len(rets)):
        vol[i] = np.std(rets[i - win:i], ddof=1)
    return np.column_stack([rets, vol])[win:]


def fit_regime_model(rets: list[float], n_states: int = 3, win: int = 20) -> dict:
    """端到端：训练 HMM 并返回状态序列/概率/参数（供 MCP 工具调用）。"""
    arr = np.asarray(rets, dtype=float)
    feats = build_features(arr, win)
    model = GaussianHMM(n_states=n_states, seed=0).fit(feats)
    path = model.decode(feats)
    gamma = model.predict_proba(feats)

    vol_state = np.sqrt(model.sigma2.sum(axis=1))
    order = np.argsort(vol_state)
    # 按波动率命名（低波→震荡，中波→趋势，高波→危机）——仅当 n_states=3 时有语义
    if n_states == 3:
        names = {order[0]: "震荡", order[1]: "趋势", order[2]: "危机"}
    else:
        names = {k: f"状态{k}" for k in range(n_states)}
    states = [names[s] for s in path]

    return {
        "n_states": n_states,
        "warmup": win,
        "states": states,
        "state_probs": [round(float(p), 4) for p in gamma[-1]],
        "transitions": [[round(float(v), 4) for v in row] for row in model.A],
        "state_params": [
            {"name": names[k],
             "mu_ret": round(float(model.mu[k][0]), 6),
             "mu_vol": round(float(model.mu[k][1]), 6),
             "vol": round(float(vol_state[k]), 6)}
            for k in range(n_states)
        ],
        "state_share": {names[k]: round(float((np.array(path) == k).mean()), 4)
                        for k in range(n_states)},
    }

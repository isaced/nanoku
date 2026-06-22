package api

import (
	"net/http"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/db/site"
	"github.com/isaced/nanoku/internal/docker"
)

type ContainerStatsDTO struct {
	Name            string  `json:"name"`
	CPUPerc         float64 `json:"cpuPerc"`
	MemUsedBytes    int64   `json:"memUsedBytes"`
	MemLimitBytes   int64   `json:"memLimitBytes"`
	MemPerc         float64 `json:"memPerc"`
	NetRxBytes      int64   `json:"netRxBytes"`
	NetTxBytes      int64   `json:"netTxBytes"`
	BlockReadBytes  int64   `json:"blockReadBytes"`
	BlockWriteBytes int64   `json:"blockWriteBytes"`
	PIDs            int     `json:"pids"`
}

type DashboardSiteDTO struct {
	ID       int    `json:"id"`
	Domain   string `json:"domain"`
	Upstream string `json:"upstream"`
	Enabled  bool   `json:"enabled"`
	AppID    *int   `json:"appId,omitempty"`
	AppName  string `json:"appName,omitempty"`
}

type DashboardAppDTO struct {
	ID          int                `json:"id"`
	Name        string             `json:"name"`
	Image       string             `json:"image"`
	Port        int                `json:"port"`
	Container   *ContainerDTO      `json:"container,omitempty"`
	SiteDomains []string           `json:"siteDomains"`
	Stats       *ContainerStatsDTO `json:"stats,omitempty"`
}

type DashboardSummary struct {
	TotalSites     int     `json:"totalSites"`
	EnabledSites   int     `json:"enabledSites"`
	TotalApps      int     `json:"totalApps"`
	RunningApps    int     `json:"runningApps"`
	TotalCPUPerc   float64 `json:"totalCpuPerc"`
	TotalMemBytes  int64   `json:"totalMemBytes"`
	TotalMemLimit  int64   `json:"totalMemLimitBytes"`
	TotalMemPerc   float64 `json:"totalMemPerc"`
	ContainerCount int     `json:"containerCount"`
}

type DashboardResponse struct {
	Summary DashboardSummary    `json:"summary"`
	Sites   []DashboardSiteDTO  `json:"sites"`
	Apps    []DashboardAppDTO   `json:"apps"`
	Stats   []ContainerStatsDTO `json:"stats"`
}

// Dashboard returns aggregated data for the home dashboard:
// all sites, all apps, and current container resource usage.
func (h *Handlers) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sites, err := h.DB.Site.Query().
		WithApp(func(q *db.AppQuery) { q.WithCurrentContainer() }).
		Order(site.ByDomain()).
		All(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	apps, err := h.DB.App.Query().Order(app.ByName()).All(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	enabledCount := 0
	siteDTOs := make([]DashboardSiteDTO, 0, len(sites))
	for _, s := range sites {
		d := DashboardSiteDTO{
			ID:       s.ID,
			Domain:   s.Domain,
			Upstream: s.Upstream,
			Enabled:  s.Enabled,
		}
		if s.Enabled {
			enabledCount++
		}
		if s.Edges.App != nil {
			id := s.Edges.App.ID
			d.AppID = &id
			d.AppName = s.Edges.App.Name
		}
		siteDTOs = append(siteDTOs, d)
	}

	appDTOs := make([]DashboardAppDTO, 0, len(apps))
	running := 0
	for _, a := range apps {
		dto := DashboardAppDTO{
			ID:    a.ID,
			Name:  a.Name,
			Image: a.Image,
			Port:  a.Port,
		}
		for _, s := range siteDTOs {
			if s.AppID != nil && *s.AppID == a.ID {
				dto.SiteDomains = append(dto.SiteDomains, s.Domain)
			}
		}
		if h.Docker != nil {
			if cur, qerr := a.QueryCurrentContainer().Only(ctx); qerr == nil && cur != nil {
				if status, serr := h.Docker.ContainerStatus(ctx, cur.Name); serr == nil && status != "not_found" {
					cur.Status = container.Status(status)
				}
				if string(cur.Status) == "running" {
					running++
				}
				c := toContainerDTO(cur)
				dto.Container = &c
			}
		}
		appDTOs = append(appDTOs, dto)
	}

	var statsDTOs []ContainerStatsDTO
	var totalCPU float64
	var totalMem, totalLimit int64

	if h.Docker != nil {
		stats, serr := h.Docker.AllStats(ctx)
		if serr == nil {
			statsByName := make(map[string]ContainerStatsDTO, len(stats))
			for _, s := range stats {
				statsByName[s.Name] = toContainerStatsDTO(s)
				totalCPU += s.CPUPerc
				totalMem += s.MemUsed
				totalLimit += s.MemLimit
			}
			if h.SelfContainer != "" {
				if selfStats, serr := h.Docker.StatsByName(ctx, h.SelfContainer); serr == nil {
					statsByName[selfStats.Name] = toContainerStatsDTO(*selfStats)
					totalCPU += selfStats.CPUPerc
					totalMem += selfStats.MemUsed
					totalLimit += selfStats.MemLimit
				}
			}
			for i := range appDTOs {
				if appDTOs[i].Container != nil {
					if st, ok := statsByName[appDTOs[i].Container.Name]; ok {
						appDTOs[i].Stats = &st
					}
				}
			}
			statsDTOs = make([]ContainerStatsDTO, 0, len(statsByName))
			for _, s := range statsByName {
				statsDTOs = append(statsDTOs, s)
			}
		}
	}

	totalMemPerc := 0.0
	if totalLimit > 0 {
		totalMemPerc = float64(totalMem) / float64(totalLimit) * 100
	}

	resp := DashboardResponse{
		Summary: DashboardSummary{
			TotalSites:     len(sites),
			EnabledSites:   enabledCount,
			TotalApps:      len(apps),
			RunningApps:    running,
			TotalCPUPerc:   totalCPU,
			TotalMemBytes:  totalMem,
			TotalMemLimit:  totalLimit,
			TotalMemPerc:   totalMemPerc,
			ContainerCount: len(statsDTOs),
		},
		Sites: siteDTOs,
		Apps:  appDTOs,
		Stats: statsDTOs,
	}
	writeJSON(w, http.StatusOK, resp)
}

func toContainerStatsDTO(s docker.ContainerStats) ContainerStatsDTO {
	return ContainerStatsDTO{
		Name:            s.Name,
		CPUPerc:         s.CPUPerc,
		MemUsedBytes:    s.MemUsed,
		MemLimitBytes:   s.MemLimit,
		MemPerc:         s.MemPerc,
		NetRxBytes:      s.NetRxBytes,
		NetTxBytes:      s.NetTxBytes,
		BlockReadBytes:  s.BlockRead,
		BlockWriteBytes: s.BlockWrite,
		PIDs:            s.PIDs,
	}
}

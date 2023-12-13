package libbox

import (
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/rw"
	"github.com/sagernet/sing/service"
)

type OutboundProvider struct {
	Tag      string
	Type     string
	IsExpand bool
	items    []*OutboundProviderItem
}

func (g *OutboundProvider) GetItems() OutboundProviderItemIterator {
	return newIterator(g.items)
}

type OutboundProviderIterator interface {
	Next() *OutboundProvider
	HasNext() bool
}

type OutboundProviderItem struct {
	Tag          string
	Type         string
	URLTestTime  int64
	URLTestDelay int32
}

type OutboundProviderItemIterator interface {
	Next() *OutboundProviderItem
	HasNext() bool
}

func (c *CommandClient) handleProviderConn(conn net.Conn) {
	defer conn.Close()

	for {
		providers, err := readProviders(conn)
		if err != nil {
			c.handler.Disconnected(err.Error())
			return
		}
		c.handler.WriteProviders(providers)
	}
}

func (s *CommandServer) handleProviderConn(conn net.Conn) error {
	defer conn.Close()
	ctx := connKeepAlive(conn)
	for {
		service := s.service
		if service != nil {
			err := writeProviders(conn, service)
			if err != nil {
				return err
			}
		} else {
			err := binary.Write(conn, binary.BigEndian, uint16(0))
			if err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.urlTestUpdate:
		}
	}
}

func readProviders(reader io.Reader) (OutboundProviderIterator, error) {
	var providerLength uint16
	err := binary.Read(reader, binary.BigEndian, &providerLength)
	if err != nil {
		return nil, err
	}

	providers := make([]*OutboundProvider, 0, providerLength)
	for i := 0; i < int(providerLength); i++ {
		var provider OutboundProvider
		provider.Tag, err = rw.ReadVString(reader)
		if err != nil {
			return nil, err
		}

		provider.Type, err = rw.ReadVString(reader)
		if err != nil {
			return nil, err
		}

		err = binary.Read(reader, binary.BigEndian, &provider.IsExpand)
		if err != nil {
			return nil, err
		}

		var itemLength uint16
		err = binary.Read(reader, binary.BigEndian, &itemLength)
		if err != nil {
			return nil, err
		}

		provider.items = make([]*OutboundProviderItem, itemLength)
		for j := 0; j < int(itemLength); j++ {
			var item OutboundProviderItem
			item.Tag, err = rw.ReadVString(reader)
			if err != nil {
				return nil, err
			}

			item.Type, err = rw.ReadVString(reader)
			if err != nil {
				return nil, err
			}

			err = binary.Read(reader, binary.BigEndian, &item.URLTestTime)
			if err != nil {
				return nil, err
			}

			err = binary.Read(reader, binary.BigEndian, &item.URLTestDelay)
			if err != nil {
				return nil, err
			}

			provider.items[j] = &item
		}
		providers = append(providers, &provider)
	}
	return newIterator(providers), nil
}

func writeProviders(writer io.Writer, boxService *BoxService) error {
	historyStorage := service.PtrFromContext[urltest.HistoryStorage](boxService.ctx)
	var cacheFile adapter.CacheFile
	if clashServer := boxService.instance.Router().ClashServer(); clashServer != nil {
		cacheFile = clashServer.CacheFile()
	}

	outbounds := boxService.instance.Router().Outbounds()
	var iProviders []adapter.OutboundProvider
	for _, it := range outbounds {
		if provider, isProvider := it.(adapter.OutboundProvider); isProvider {
			iProviders = append(iProviders, provider)
		}
	}
	var providers []OutboundProvider
	for _, iProvider := range iProviders {
		var provider OutboundProvider
		provider.Tag = iProvider.Tag()
		provider.Type = iProvider.Type()
		if cacheFile != nil {
			if isExpand, loaded := cacheFile.LoadGroupExpand(provider.Tag); loaded {
				provider.IsExpand = isExpand
			}
		}

		for _, outbound := range iProvider.Outbounds() {
			var item OutboundProviderItem
			item.Tag = outbound.Tag()
			item.Type = outbound.Type()
			if history := historyStorage.LoadURLTestHistory(outbound.Tag()); history != nil {
				item.URLTestTime = history.Time.Unix()
				item.URLTestDelay = int32(history.Delay)
			}
			provider.items = append(provider.items, &item)
		}
		providers = append(providers, provider)
	}

	err := binary.Write(writer, binary.BigEndian, uint16(len(providers)))
	if err != nil {
		return err
	}
	for _, provider := range providers {
		err = rw.WriteVString(writer, provider.Tag)
		if err != nil {
			return err
		}
		err = rw.WriteVString(writer, provider.Type)
		if err != nil {
			return err
		}
		err = binary.Write(writer, binary.BigEndian, provider.IsExpand)
		if err != nil {
			return err
		}
		err = binary.Write(writer, binary.BigEndian, uint16(len(provider.items)))
		if err != nil {
			return err
		}
		for _, item := range provider.items {
			err = rw.WriteVString(writer, item.Tag)
			if err != nil {
				return err
			}
			err = rw.WriteVString(writer, item.Type)
			if err != nil {
				return err
			}
			err = binary.Write(writer, binary.BigEndian, item.URLTestTime)
			if err != nil {
				return err
			}
			err = binary.Write(writer, binary.BigEndian, item.URLTestDelay)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *CommandClient) SetProviderExpand(providerTag string, isExpand bool) error {
	conn, err := c.directConnect()
	if err != nil {
		return err
	}
	defer conn.Close()
	err = binary.Write(conn, binary.BigEndian, uint8(CommandGroupExpand))
	if err != nil {
		return err
	}
	err = rw.WriteVString(conn, providerTag)
	if err != nil {
		return err
	}
	err = binary.Write(conn, binary.BigEndian, isExpand)
	if err != nil {
		return err
	}
	return readError(conn)
}

func (s *CommandServer) handleSetProviderExpand(conn net.Conn) error {
	defer conn.Close()
	providerTag, err := rw.ReadVString(conn)
	if err != nil {
		return err
	}
	var isExpand bool
	err = binary.Read(conn, binary.BigEndian, &isExpand)
	if err != nil {
		return err
	}
	service := s.service
	if service == nil {
		return writeError(conn, E.New("service not ready"))
	}
	if clashServer := service.instance.Router().ClashServer(); clashServer != nil {
		if cacheFile := clashServer.CacheFile(); cacheFile != nil {
			err = cacheFile.StoreGroupExpand(providerTag, isExpand)
			if err != nil {
				return writeError(conn, err)
			}
		}
	}
	return writeError(conn, nil)
}

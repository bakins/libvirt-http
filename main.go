package main

import (
	"encoding/xml"
	"fmt"
	"github.com/alexzorin/libvirt-go"
	"github.com/gin-gonic/gin"
	"runtime"
	"strings"
)

type (
	DiskDriver struct {
		Name string `xml:"name,attr" json:"name"`
		Type string `xml:"type,attr" json:"type"`
	}

	DiskSource struct {
		File   string `xml:"file,omitempty,attr" json:"file,omitempty"`
		Device string `xml:"dev,omitempty,attr" json:"device,omitempty"`
	}

	DiskTarget struct {
		Dev string `xml:"dev,attr" json:"dev"`
		Bus string `xml:"bus,attr" json:"bus"`
	}

	Disk struct {
		Type   string     `xml:"type,attr"  json:"type"`
		Device string     `xml:"device,attr"  json:"device"`
		Driver DiskDriver `xml:"driver"  json:"driver"`
		Source DiskSource `xml:"source" json:"source"`
		Target DiskTarget `xml:"target" json:"target"`
	}

	InterfaceSource struct {
		Network string `xml:"network,omitempty,attr" json:"network,omitempty"`
		Bridge  string `xml:"bridge,omitempty,attr" json:"bridge,omitempty"`
	}

	InterfaceMac struct {
		Address string `xml:"address,attr" json:"address"`
	}

	InterfaceModel struct {
		Type string `xml:"type,omitempty,attr" json:"type,omitempty"`
	}

	FilterRefParameter struct {
		Name  string `xml:"name,attr" json:"name"`
		Value string `xml:"value,attr" json:"value"`
	}

	FilterRef struct {
		Filter     string               `xml:"filter,attr"  json:"filter"`
		Parameters []FilterRefParameter `xml:"parameter" json:"parameters"`
	}

	Interface struct {
		Type      string          `xml:"type,attr"  json:"type"`
		Source    InterfaceSource `xml:"source,omitempty" json:"source,omitempty"`
		Mac       InterfaceMac    `xml:"mac,omitempty" json:"mac,omitempty"`
		Model     InterfaceModel  `xml:"model,omitempty" json:"model,omitempty"`
		FilterRef FilterRef       `xml:"filterref,omitempty" json:"filterref,omitempty"`
	}

	Device struct {
		Disks      []Disk      `xml:"disk" json:"disks"`
		Interfaces []Interface `xml:"interface" json:"interfaces"`
	}

	OsType struct {
		Type    string `xml:",chardata" json:"type,omitempty"`
		Arch    string `xml:"arch,attr,omitempty" json:"arch,omitempty"`
		Machine string `xml:"machine,attr,omitempty" json:"machine,omitempty"`
	}

	OsBoot struct {
		Dev string `xml:"dev,attr,omitempty" json:"dev,omitempty"`
	}

	Os struct {
		Type OsType `xml:"type,omitempty" json:"type,omitempty"`
		Boot OsBoot `xml:"boot,omitempty" json:"boot,omitempty"`
	}

	Domain struct {
		XMLName struct{} `xml:"domain" json:"-"`
		Type    string   `xml:"type,attr" json:"type"`
		UUID    string   `xml:"uuid" json:"uuid"`
		Name    string   `xml:"name" json:"name"`
		Memory  int      `xml:"memory" json:"memory"`
		VCPU    int      `xml:"vcpu" json:"vpcu"`
		Devices Device   `xml:"devices,omitempty" json:"devices"`
		Os      Os       `xml:"os,omitempty" json:"os"`
		domain  *libvirt.VirDomain
	}

	ErrorMsg struct {
		Error string `json:"error"`
	}

	Context struct {
		*gin.Context
		V        *libvirt.VirConnection
		freelist []Freer
	}

	HandlerFunc func(*Context) error

	Freer interface {
		Free()
	}
)

func (d *Domain) Free() {
	if d.Name != "" {
		fmt.Printf("Freeing: %s\n", d.Name)
		d.domain.Free()
		d.Name = ""
	}
}

func (c *Context) FreeList(f Freer) {
	c.freelist = append(c.freelist, f)
}

/*
func (d *Domain) MarshalJSON() ([]byte, error) {
	fmt.Printf("MarshalJSON: %s\n", d.Name)
	return json.Marshal(d)
}
*/
func buildDomain(dom *libvirt.VirDomain) (*Domain, error) {
	xmldesc, err := dom.GetXMLDesc(0)
	if err != nil {
		return nil, err
	}

	d := new(Domain)
	err = xml.Unmarshal([]byte(xmldesc), d)
	if err != nil {
		return nil, err
	}

	d.domain = dom
	runtime.SetFinalizer(d, func(d *Domain) {
		d.Free()
	})
	return d, nil
}

func listDomains(c *Context) error {
	doms, err := c.V.ListAllDomains(0)
	if err != nil {
		return c.JSONError(500, err)
	}
	domains := make([]*Domain, len(doms))
	for i, _ := range doms {
		d, err := buildDomain(&doms[i])
		if err != nil {
			return c.JSONError(500, err)
		}
		c.FreeList(d)
		domains[i] = d
	}
	c.JSON(200, domains)
	return nil
}

func getDomain(c *Context) error {
	name := c.Params.ByName("name")
	dom, err := c.V.LookupDomainByName(name)

	if err != nil {
		code := 500
		if strings.Contains(err.Error(), "Domain not found") {
			code = 404
		}
		return c.JSONError(code, err)
	}
	d, err := buildDomain(&dom)
	if err != nil {
		return c.JSONError(500, err)
	}
	c.JSON(200, d)
	return nil
}

func withContext(fn HandlerFunc) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		v, err := libvirt.NewVirConnection("qemu:///system")
		if err != nil {
			c.Abort(500)
		}
		defer v.CloseConnection()

		ctx := &Context{
			Context:  c,
			V:        &v,
			freelist: make([]Freer, 0),
		}
		defer func() {
			for _, f := range ctx.freelist {
				f.Free()
			}
		}()
		fn(ctx)
	})
}

func (c *Context) JSONError(code int, err error) error {
	c.JSON(code, ErrorMsg{Error: err.Error()})
	return err
}

func main() {
	r := gin.Default()
	r.GET("/ping", func(c *gin.Context) {
		c.String(200, "pong")
	})

	domains := r.Group("/domains")
	{
		domains.GET("", withContext(listDomains))
		domains.GET(":name", withContext(getDomain))
	}

	// Listen and server on 0.0.0.0:8080
	r.Run(":8080")
}

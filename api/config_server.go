package api

import (
	"context"
	"crypto/tls"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/pomerium/cli/proto"
)

func (s *server) List(_ context.Context, sel *pb.Selector) (*pb.Records, error) {
	s.Lock()
	defer s.Unlock()

	records, err := s.listLocked(sel)
	if err != nil {
		return nil, err
	}
	return &pb.Records{Records: records}, nil
}

func (s *server) listLocked(sel *pb.Selector) ([]*pb.Record, error) {
	var records []*pb.Record
	var err error
	if sel.GetAll() {
		records, err = s.config.listAll(), nil
	} else if len(sel.GetIds()) > 0 {
		records, err = s.config.listByIDs(sel.GetIds())
	} else if len(sel.GetTags()) > 0 {
		records, err = s.config.listByTags(sel.GetTags())
	} else {
		return nil, status.Error(codes.InvalidArgument, "either all, ids or tags filter must be specified")
	}
	if err != nil {
		return nil, err
	}
	return withCertInfo(s.certInfo, records), nil
}

func (s *server) Delete(_ context.Context, sel *pb.Selector) (*pb.DeleteRecordsResponse, error) {
	s.Lock()
	defer s.Unlock()

	recs, err := s.listLocked(sel)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(recs))
	for _, r := range recs {
		ids = append(ids, r.GetId())
	}
	if _, err = s.disconnectLocked(ids); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	for _, id := range ids {
		if err = s.config.delete(id); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &pb.DeleteRecordsResponse{}, nil
}

func (s *server) Upsert(_ context.Context, r *pb.Record) (*pb.Record, error) {
	s.Lock()
	defer s.Unlock()

	if r.Conn != nil && r.Conn.ClientCert != nil {
		_, err := tls.X509KeyPair(r.Conn.ClientCert.Cert, r.Conn.ClientCert.Key)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("client cert: %s", err.Error()))
		}
		info, err := getCertInfo(s.certInfo, r.Conn.ClientCert.Cert)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("client cert info: %s", err.Error()))
		}
		r.Conn.ClientCert.Info = info
	}
	if err := s.config.clearTags(r); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.config.upsert(r)
	if err := s.config.save(s.ConfigProvider); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return r, nil
}

func (s *server) Export(ctx context.Context, req *pb.ExportRequest) (*pb.ConfigData, error) {
	s.RLock()
	defer s.RUnlock()

	recs, err := s.listLocked(req.Selector)
	if err != nil {
		return nil, err
	}

	opts := protojson.MarshalOptions{}
	if req.Format == pb.ExportRequest_EXPORT_FORMAT_JSON_PRETTY {
		opts.Multiline = true
		opts.Indent = "  "
	}
	data, err := exportRecords(recs, req.RemoveTags, opts)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.ConfigData{Data: data}, nil
}

func (s *server) Import(_ context.Context, req *pb.ImportRequest) (*pb.ImportResponse, error) {
	s.Lock()
	defer s.Unlock()

	if err := importRecords(s.config, req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.ImportResponse{}, nil
}

func (s *server) GetTags(_ context.Context, req *pb.GetTagsRequest) (*pb.GetTagsResponse, error) {
	s.RLock()
	defer s.RUnlock()

	return &pb.GetTagsResponse{Tags: s.config.getTags()}, nil
}
